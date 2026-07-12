package main

import (
	"log"
	"net/http"
	"os"

	"github.com/swaroop/aero/aerocore/internal/registry"
	"github.com/swaroop/aero/aerocore/internal/server"
)

func main() {
	addr := os.Getenv("AEROCORE_ADDR")
	if addr == "" {
		addr = ":8088"
	}

	reg := registry.NewMemoryRegistry()
	srv := server.New(reg)

	log.Printf("aerocore listening on %s", addr)
	if err := http.ListenAndServe(addr, srv); err != nil {
		log.Fatal(err)
	}
}
