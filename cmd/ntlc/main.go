package main

import (
	"encoding/gob"
	"flag"
	"log"
	"nautrouds/internal/compiler"
	"os"
	"path/filepath"
)

func main() {
	input := flag.String("i", "Ntufile", "Path to Ntufile (use '-' for stdin)")
	output := flag.String("o", "nautrouds.ntu", "Output compiled route file")
	check := flag.Bool("check", false, "")
	print := flag.Bool("print", false, "")

	flag.Parse()

	var reader *os.File
	var err error

	// 1. Open Source Stream
	if *input == "-" {
		reader = os.Stdin
	} else {
		reader, err = os.Open(*input)
		if err != nil {
			log.Fatalf("Failed to open Ntufile: %v", err)
		}
		defer reader.Close()
	}

	// 2. Compile directly from Reader (Streaming)
	tree, err := compiler.Parse(reader)
	if err != nil {
		log.Fatalf("Compilation Error: %v", err)
	}

	if *print {
		tree.PrintTree()
	}

	if *check {
		log.Println("Successfully compiled")
		return
	}

	if *output == "-" {
		enc := gob.NewEncoder(os.Stdout)
		if err := enc.Encode(tree); err != nil {
			log.Fatalf("Serialization Error: %v", err)
		}
		return
	}

	{
		state, err := os.Stat(*output)
		if err == nil && state.IsDir() {
			*output = filepath.Join(*output, "nautrouds.ntu")
		} else if err != nil && !os.IsNotExist(err) {
			log.Fatalf("Failed to stat output file: %v", err)
		}

	}

	temp := *output + ".tmp"
	defer os.Remove(temp)

	// 3. Serialize to GOB
	file, err := os.Create(temp)
	if err != nil {
		log.Fatalf("Failed to create output file: %v", err)
	}
	defer file.Close()

	enc := gob.NewEncoder(file)
	if err := enc.Encode(tree); err != nil {
		log.Fatalf("Serialization Error: %v", err)
	}

	if err := os.Remove(*output); err != nil && !os.IsNotExist(err) {
		log.Fatalf("Failed to remove output file: %v", err)
	}

	if err := os.Rename(temp, *output); err != nil {
		log.Fatalf("Failed to rename output file: %v", err)
	}

	log.Printf("Successfully compiled input to %s", *output)
}
