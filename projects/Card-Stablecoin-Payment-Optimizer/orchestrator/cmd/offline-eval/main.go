// Command offline-eval runs milestone O1: it evaluates a versioned dataset of
// recorded baskets and checks every result against its expectation.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/engine"
	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/offline"
)

func main() {
	dataset := flag.String("dataset", "fixtures/recorded/v1", "dataset directory")
	engineURL := flag.String("engine-url", "http://127.0.0.1:8081", "engine base URL")
	asJSON := flag.Bool("json", false, "print the full report as JSON")
	flag.Parse()

	token := os.Getenv("ENGINE_AUTH_TOKEN")
	if token == "" {
		fmt.Fprintln(os.Stderr, "offline-eval: ENGINE_AUTH_TOKEN is required")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	report, err := offline.Run(ctx, engine.NewClient(*engineURL, token), *dataset)
	if err != nil {
		fmt.Fprintln(os.Stderr, "offline-eval:", err)
		os.Exit(1)
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(report) //nolint:errcheck
	} else {
		for _, c := range report.Cases {
			status := "ok  "
			if !c.Passed {
				status = "FAIL"
			}
			fmt.Printf("%s %-44s %s %s\n", status, c.Name, c.Outcome, c.Error)
			if !c.Passed && c.Expected != nil {
				expected, _ := json.Marshal(c.Expected)
				actual, _ := json.Marshal(c.Actual)
				fmt.Printf("     expected %s\n     actual   %s\n", expected, actual)
			}
		}
		fmt.Printf("\n%d baskets: %d matched, %d failed; %d produced a recommendation (%v)\n",
			report.Total, report.Passed, report.Failed, report.Recommended, report.ByOutcome)
	}
	if report.Failed > 0 {
		os.Exit(1)
	}
}
