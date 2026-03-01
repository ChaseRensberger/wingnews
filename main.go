package main

import (
	"log"
	"net/http"
)

func main() {
	s := newServer()
	r := newRouter(s)

	log.Println("wingnews listening on :3000")
	if err := http.ListenAndServe(":3000", r); err != nil {
		log.Fatal(err)
	}
}
