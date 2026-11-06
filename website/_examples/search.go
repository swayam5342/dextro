package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
)

func main() {
	base, key := os.Getenv("TRIBUTARY_URL"), os.Getenv("TRIBUTARY_KEY")

	query := url.Values{"q": {"report"}}
	req, _ := http.NewRequest("GET", base+"/api/v1/search?"+query.Encode(), nil)
	req.Header.Set("Authorization", "Bearer "+key)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	defer res.Body.Close()

	var results struct {
		Files []struct{ ID, Name string }
	}
	if err := json.NewDecoder(res.Body).Decode(&results); err != nil {
		panic(err)
	}
	for _, f := range results.Files {
		fmt.Println(f.Name, f.ID)
	}
}
