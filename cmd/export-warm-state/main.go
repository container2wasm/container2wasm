package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/ktock/container2wasm/cmd/warmstate"
)

func main() {
	var (
		input      = flag.String("input", "", "raw wasm linear memory dump")
		output     = flag.String("output", "warm.state", "output warm.state path")
		sourceHash = flag.String("source-hash", "", "VM sourceHash")
		ociDigest  = flag.String("oci-digest", "", "OCI config digest")
		vmMemoryMb = flag.Int("vm-memory-mb", 1024, "guest VM memory MB")
		guestOffset = flag.Int("guest-ram-offset", 0, "guest RAM offset in linear memory")
	)
	flag.Parse()
	if *input == "" {
		fmt.Fprintln(os.Stderr, "specify --input memory dump")
		os.Exit(1)
	}
	payload, err := os.ReadFile(*input)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	header := warmstate.Header{
		MarkerVersion:  warmstate.MarkerVersion,
		SourceHash:     *sourceHash,
		OciDigest:      *ociDigest,
		VMMemorySizeMb: *vmMemoryMb,
		GuestRamOffset: *guestOffset,
		GuestRamSize:   len(payload),
	}
	if err := warmstate.Write(*output, header, payload); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("wrote", *output, len(payload), "bytes")
}
