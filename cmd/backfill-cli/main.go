// backfill-cli is a one-shot CLI that paginates the VCS history and
// ingests historical review comments for retrieval. Slice 3 contains
// the real implementation.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "backfill-cli: not yet runnable; slice 3 adds the historical-comment ingestion")
	os.Exit(0)
}
