// rules-sync clones the configured rules repo and upserts rule rows
// into the database. Slice 5 contains the real implementation.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "rules-sync: not yet runnable; slice 5 adds the git clone + parse + upsert pipeline")
	os.Exit(0)
}
