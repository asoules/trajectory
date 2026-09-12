# Bundled SQLite

SQLite 3.53.4 is compiled directly into the Go executable through cgo.
There is no dependency on a system SQLite library or third-party Go modules.
Building requires Go 1.22+ and a C compiler; running requires neither toolchain.

Source: https://www.sqlite.org/2026/sqlite-amalgamation-3530400.zip
Archive SHA3-256: 628a44cfe82c66aed1ccbbe85a562d2e33ebe64b3288981ed76285612227934e

`sqlite3.c` and `sqlite3.h` are unmodified upstream amalgamation files.
SQLite is public domain: https://www.sqlite.org/copyright.html

Compile options: SQLITE_THREADSAFE=1, SQLITE_OMIT_LOAD_EXTENSION.
To update, download a release from sqlite.org, verify its published checksum,
replace both amalgamation files, update this document, and run all Go tests.
