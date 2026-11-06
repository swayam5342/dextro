package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
)

func main() {
	base, key := os.Getenv("TRIBUTARY_URL"), os.Getenv("TRIBUTARY_KEY")
	fileID := os.Getenv("FILE_ID")

	send := func(method, path string, body any) *http.Response {
		data, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, base+path, bytes.NewReader(data))
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			panic(err)
		}
		if res.StatusCode >= 300 {
			panic(res.Status)
		}
		return res
	}

	res := send("POST", "/api/v1/folders", map[string]any{"name": "Reports", "parent_id": nil})
	defer res.Body.Close()
	var folder struct{ ID string }
	if err := json.NewDecoder(res.Body).Decode(&folder); err != nil {
		panic(err)
	}

	send("POST", "/api/v1/files/"+fileID+"/move", map[string]any{"folder_id": folder.ID}).Body.Close()
	send("PATCH", "/api/v1/files/"+fileID, map[string]any{"name": "March report.pdf"}).Body.Close()
}
