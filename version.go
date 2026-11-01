package main

// version is the app version. When releasing, it must equal the tag (vX.Y.Z) and the versions in
// winres/winres.json; `go run ./tools/release --check X.Y.Z` verifies this (CI runs it on tags).
const version = "1.0.0"
