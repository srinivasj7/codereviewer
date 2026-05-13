// indexer-worker consumes IndexJob messages and indexes default-branch
// pushes into code_chunks. Slice 1 contains the real implementation.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "indexer-worker: not yet runnable; slice 1 adds the parser + storepostgres adapters")
	os.Exit(0)
}
