// Command debug-check-drives runs the check_drives skill directly, outside
// the agent loop, so its raw output (and any error) can be inspected without
// waiting for the disk-space-check responsibility to fire.
//
// Usage: go run ./endpoint-server/cmd/debug-check-drives
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"agent_patches/endpoint-server/skills/check_drives"
)

func main() {
	tl, err := check_drives.NewDiskUsageTool()
	if err != nil {
		fmt.Println("NewDiskUsageTool error:", err)
		return
	}

	start := time.Now()
	input, _ := json.Marshal(struct{}{})
	result, err := tl.Execute(context.Background(), input)
	elapsed := time.Since(start)

	fmt.Println("elapsed:", elapsed)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("output length:", len(result))
	fmt.Println("---")
	fmt.Println(result)
}
