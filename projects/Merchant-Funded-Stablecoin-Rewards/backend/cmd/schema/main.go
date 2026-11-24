package main

import (
	"encoding/json"
	"fmt"
	"os"

	"unowallet/rewards/internal/rewards"
)

func main() {
	spec := rewards.BuildOpenAPISpec()
	b, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal openapi: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(b))
}
