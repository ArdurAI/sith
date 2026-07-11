// SPDX-License-Identifier: Apache-2.0

// Command fixture is a deterministic in-cluster target for Sith's real streaming tests.
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "fail" {
		fmt.Fprintln(os.Stderr, "sith fixture intentional failure")
		os.Exit(1)
	}
	if len(os.Args) > 1 && os.Args[1] == "echo" {
		fmt.Println(strings.Join(os.Args[2:], " "))
		return
	}
	cluster := strings.Map(func(character rune) rune {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || strings.ContainsRune("-_.", character) {
			return character
		}
		return '_'
	}, os.Getenv("SITH_FIXTURE_CLUSTER"))
	// #nosec G706 -- cluster is reduced to the allowlist above before entering the log line.
	log.Printf("sith fixture cluster=%s ready", cluster)
	handler := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = fmt.Fprintf(response, "sith fixture cluster=%s\n", cluster)
	})
	server := &http.Server{Addr: ":8080", Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
