package main

import (
	"log"
	"net/http"
	"os"

	"github.com/vancemichael/092002-medical-collaboration-control/internal/httpapi"
	"github.com/vancemichael/092002-medical-collaboration-control/internal/store"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	dbPath := os.Getenv("DATABASE_PATH")
	if dbPath == "" {
		dbPath = "data/app.sqlite3"
	}
	st, err := store.Open(dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()
	log.Fatal(http.ListenAndServe(":"+port, httpapi.Router(st)))
}
