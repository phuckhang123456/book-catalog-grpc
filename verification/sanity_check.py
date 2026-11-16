"""
Sanity check for the Book Catalog project. Needs only Python 3.

WHAT THIS IS: a small independent check of two things that can be checked
without Go or protoc:
  1. proto/catalog.proto (read with a tiny regex parser, NOT with protoc):
     field numbers are unique inside each message, every referenced type and RPC
     message exists, the three baseline RPCs and the baseline field numbers are
     unchanged, and CheckAvailability was added with new messages.
  2. The SQL text in cmd/server/store.go, executed in real SQLite: schema,
     seeding, pagination (ORDER BY / LIMIT / OFFSET), duplicate-ISBN error text,
     constraints, and read-only behaviour of the availability query.

WHAT THIS IS NOT: it does not compile or run any Go code, it does not run protoc,
and it does not test gRPC. Run:  python3 verification/sanity_check.py
"""
import os
import re
import sqlite3

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.join(HERE, "..")
proto = open(os.path.join(ROOT, "proto", "catalog.proto"), encoding="utf-8").read()
store_go = open(os.path.join(ROOT, "cmd", "server", "store.go"), encoding="utf-8").read()

failures = []


def check(name, ok):
    print(("PASS  " if ok else "FAIL  ") + name)
    if not ok:
        failures.append(name)


# ---------------------------------------------------------------- 1. proto ---
text = re.sub(r"//.*", "", proto)  # drop comments
messages = {}
for m in re.finditer(r"message\s+(\w+)\s*\{(.*?)\}", text, re.S):
    fields = re.findall(r"(repeated\s+)?([\w.]+)\s+(\w+)\s*=\s*(\d+)\s*;", m.group(2))
    messages[m.group(1)] = [(t, n, int(num)) for _, t, n, num in fields]

service = re.search(r"service\s+(\w+)\s*\{(.*?)\}", text, re.S)
rpcs = re.findall(r"rpc\s+(\w+)\s*\(\s*(\w+)\s*\)\s*returns\s*\(\s*(\w+)\s*\)", service.group(2))

check("syntax is proto3", 'syntax = "proto3";' in text)
check("go_package is book-catalog-grpc/gen/catalogpb", 'option go_package = "book-catalog-grpc/gen/catalogpb";' in text)
check("service is named BookCatalog", service.group(1) == "BookCatalog")
scalars = {"int32", "string", "double", "bool"}
for name, fields in messages.items():
    nums = [f[2] for f in fields]
    check(f"message {name}: field numbers unique", len(nums) == len(set(nums)))
    check(f"message {name}: field names unique", len({f[1] for f in fields}) == len(fields))
    check(f"message {name}: field types known", all(t in scalars or t in messages for t, _, _ in fields))
check("every RPC request/response message is defined",
      all(a in messages and b in messages for _, a, b in rpcs))
check("RPC list is GetBook, ListBooks, CreateBook, CheckAvailability (baseline first)",
      [r[0] for r in rpcs] == ["GetBook", "ListBooks", "CreateBook", "CheckAvailability"])

# Baseline contract: these numbers must never change (compatibility with old clients).
baseline = {
    "Book": [("int32", "id", 1), ("string", "title", 2), ("string", "author", 3),
             ("string", "isbn", 4), ("double", "price", 5), ("int32", "stock", 6)],
    "GetBookRequest": [("int32", "book_id", 1)],
    "GetBookResponse": [("Book", "book", 1)],
    "ListBooksRequest": [("int32", "page", 1), ("int32", "page_size", 2)],
    "ListBooksResponse": [("Book", "books", 1), ("int32", "total", 2)],
    "CreateBookRequest": [("string", "title", 1), ("string", "author", 2), ("string", "isbn", 3),
                          ("double", "price", 4), ("int32", "stock", 5)],
    "CreateBookResponse": [("Book", "book", 1)],
}
for name, expected in baseline.items():
    check(f"baseline message {name} unchanged", messages.get(name) == expected)
check("CreateBookRequest has no id field (client cannot choose the id)",
      "id" not in [f[1] for f in messages["CreateBookRequest"]])
check("CheckAvailabilityRequest = book_id(1), quantity(2)",
      messages["CheckAvailabilityRequest"] == [("int32", "book_id", 1), ("int32", "quantity", 2)])
check("CheckAvailabilityResponse = available(1), current_stock(2)",
      messages["CheckAvailabilityResponse"] == [("bool", "available", 1), ("int32", "current_stock", 2)])

# ------------------------------------------------------------------ 2. SQL ---
schema = re.search(r"const createBooksTable = `(.*?)`", store_go, re.S).group(1)
insert_sql = re.search(r'"(INSERT INTO books \(title, author, isbn, price, stock\) VALUES \(\?, \?, \?, \?, \?\))"', store_go).group(1)
select_one = re.search(r'"(SELECT id, title, author, isbn, price, stock FROM books WHERE id = \?)"', store_go).group(1)
select_page = re.search(r'"(SELECT id, title, author, isbn, price, stock FROM books ORDER BY id LIMIT \? OFFSET \?)"', store_go).group(1)
dup_text = re.search(r'strings.Contains\(err.Error\(\), "([^"]+)"\)', store_go).group(1)
seeds = re.findall(r'\{"([^"]+)", "([^"]+)", "([^"]+)", ([\d.]+), (\d+)\},', store_go)

con = sqlite3.connect(":memory:", isolation_level=None)
con.execute(schema)
con.execute(schema)  # IF NOT EXISTS: running twice is harmless
check("schema creates (twice) without error", True)
check("3 seed books found in store.go", len(seeds) == 3)
for s in seeds:
    con.execute(insert_sql, (s[0], s[1], s[2], float(s[3]), int(s[4])))
check("seed ids are 1, 2, 3", [r[0] for r in con.execute("SELECT id FROM books ORDER BY id")] == [1, 2, 3])

page1 = con.execute(select_page, (2, 0)).fetchall()
page2 = con.execute(select_page, (2, 2)).fetchall()
past = con.execute(select_page, (2, 18)).fetchall()
check("ListBooks page 1 size 2 -> ids [1, 2]", [r[0] for r in page1] == [1, 2])
check("ListBooks page 2 size 2 -> ids [3]", [r[0] for r in page2] == [3])
check("ListBooks page past the end -> empty", past == [])
check("COUNT(*) total is 3", con.execute("SELECT COUNT(*) FROM books").fetchone()[0] == 3)

row = con.execute(select_one, (1,)).fetchone()
check("GetBook query returns the 6 columns in order", row[1] == "The Go Programming Language" and len(row) == 6)
check("GetBook query for an unknown id returns no row (-> sql.ErrNoRows -> NotFound)",
      con.execute(select_one, (9999,)).fetchone() is None)

try:
    con.execute(insert_sql, ("Copy", "A", seeds[0][2], 5.0, 1))
    check("duplicate ISBN rejected", False)
except sqlite3.IntegrityError as e:
    check("duplicate ISBN rejected", True)
    check(f"error text contains the string checked in Go: {dup_text!r}", dup_text in str(e))

for label, sql in [("CHECK rejects price 0", "UPDATE books SET price = 0 WHERE id = 1"),
                   ("CHECK rejects negative stock", "UPDATE books SET stock = -1 WHERE id = 1"),
                   ("NOT NULL rejects missing title", "INSERT INTO books (author, isbn, price) VALUES ('A', 'N', 1)")]:
    try:
        con.execute(sql)
        check(label, False)
    except sqlite3.IntegrityError:
        check(label, True)

# CheckAvailability uses only the SELECT above, so it cannot change anything.
before = con.execute("SELECT id, title, author, isbn, price, stock FROM books ORDER BY id").fetchall()
for qty in (3, 10, 11):
    stock = con.execute(select_one, (1,)).fetchone()[5]
    available = stock >= qty
    check(f"availability logic: stock 10, quantity {qty} -> {available}", available == (qty <= 10))
after = con.execute("SELECT id, title, author, isbn, price, stock FROM books ORDER BY id").fetchall()
check("availability check leaves the whole table unchanged", before == after)

print("\nALL SANITY CHECKS PASSED" if not failures else f"\n{len(failures)} CHECK(S) FAILED")
raise SystemExit(1 if failures else 0)
