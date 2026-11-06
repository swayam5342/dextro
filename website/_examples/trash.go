package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
)

func main() {
	base, key := os.Getenv("TRIBUTARY_URL"), os.Getenv("TRIBUTARY_KEY")
	fileID := os.Getenv("FILE_ID")

	send := func(method, path string) *http.Response {
		req, _ := http.NewRequest(method, base+path, nil)
		req.Header.Set("Authorization", "Bearer "+key)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			panic(err)
		}
		if res.StatusCode >= 300 {
			panic(res.Status)
		}
		return res
	}

	send("POST", "/api/v1/files/"+fileID+"/trash").Body.Close()

	res := send("GET", "/api/v1/trash")
	defer res.Body.Close()
	var trash struct{ Files []struct{ Name string } }
	if err := json.NewDecoder(res.Body).Decode(&trash); err != nil {
		panic(err)
	}
	fmt.Println(len(trash.Files), "files in the trash")

	send("POST", "/api/v1/files/"+fileID+"/restore").Body.Close()
}
