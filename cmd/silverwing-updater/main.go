package main

import (
	"context"
	"flag"
	"log"

	"silverwing-go/internal/appupdate/native"
)

func main() {
	job := flag.String("job", "", "prepared application update")
	flag.Parse()
	if *job == "" {
		log.Fatal("missing update handoff")
	}
	if err := native.RunHelper(context.Background(), *job); err != nil {
		log.Fatal(err)
	}
}
