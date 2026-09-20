package main

import (
	"log"
	"net/http"
	"os"

	"github.com/vancemichael/092002-medical-collaboration-control/internal/httpapi"
	"github.com/vancemichael/092002-medical-collaboration-control/internal/terminology"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	databasePath := os.Getenv("DATABASE_PATH")
	if databasePath == "" {
		databasePath = "data/app.sqlite3"
	}
	store, err := terminology.Open(databasePath)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	service := terminology.NewService(store)
	log.Fatal(http.ListenAndServe(":"+port, httpapi.New(service)))
}
