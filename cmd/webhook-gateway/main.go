// webhook-gateway receives VCS webhooks over HTTPS, verifies HMAC,
// and enqueues jobs to the bus. Slice 0 contains only the wiring
// skeleton; the real HTTP listener and HMAC verification land in
// slice 1 alongside the vcsgithub adapter.
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	cfgPath := flag.String("config", "config.toml", "path to TOML config file")
	flag.Parse()

	fmt.Fprintf(os.Stderr,
		"webhook-gateway: not yet runnable (slice 1 adds the chi listener + HMAC verify);\n"+
			"config path was %q\n", *cfgPath)
	os.Exit(0)
}
