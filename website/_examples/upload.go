package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
)

func main() {
	base, key := os.Getenv("TRIBUTARY_URL"), os.Getenv("TRIBUTARY_KEY")

	file, err := os.Open("report.pdf")
	if err != nil {
		panic(err)
	}
	defer file.Close()
	info, _ := file.Stat()

	req, _ := http.NewRequest("POST", base+"/api/v1/files?name=report.pdf", file)
	req.ContentLength = info.Size()
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/pdf")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		panic(res.Status)
	}

	var stored struct{ ID string }
	if err := json.NewDecoder(res.Body).Decode(&stored); err != nil {
		panic(err)
	}
	fmt.Println(stored.ID)
}
