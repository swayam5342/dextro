package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
)

func main() {
	base, key := os.Getenv("TRIBUTARY_URL"), os.Getenv("TRIBUTARY_KEY")

	req, _ := http.NewRequest("GET", base+"/api/v1/files", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	defer res.Body.Close()

	var listing struct {
		Folders []struct{ ID, Name string }
		Files   []struct {
			ID, Name string
			Size     int64
		}
	}
	if err := json.NewDecoder(res.Body).Decode(&listing); err != nil {
		panic(err)
	}
	for _, f := range listing.Folders {
		fmt.Println("folder", f.Name, f.ID)
	}
	for _, f := range listing.Files {
		fmt.Println("file", f.Name, f.Size, f.ID)
	}
}
