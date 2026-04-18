package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	imageroutes "rmfosho/cosmodrome-image-server/src/routes/image"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/joho/godotenv"
)

func getRandomCat() (string, error) {
	resp, err := http.Get("https://api.thecatapi.com/v1/images/search")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	// get [0].url from the response
	type catResponse struct {
		URL string `json:"url"`
	}
	var cats []catResponse
	err = json.NewDecoder(resp.Body).Decode(&cats)
	if err != nil {
		return "", err
	}
	return cats[0].URL, nil
}

func main() {
	// load env vars
	err := godotenv.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error loading .env file")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	imageroutes.Register(r)

	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		catURL, err := getRandomCat()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/html")

		w.Write([]byte(fmt.Sprintf(`<img src="%s" alt="Random Cat" style="max-width: 100%%; height: auto;" />`, catURL)))
	})

	fmt.Printf("listening on :%s\n", port)
	if err := http.ListenAndServe(":"+port, r); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
