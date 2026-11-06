package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

// Every piece but the last must be a multiple of 256 KiB.
const piece = 8 << 20

type session struct {
	ID       string
	Received int64
	File     *struct{ ID string }
}

func main() {
	base, key := os.Getenv("TRIBUTARY_URL"), os.Getenv("TRIBUTARY_KEY")

	send := func(method, path string, body io.Reader) session {
		req, _ := http.NewRequest(method, base+path, body)
		req.Header.Set("Authorization", "Bearer "+key)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			panic(err)
		}
		defer res.Body.Close()
		if res.StatusCode >= 300 {
			panic(res.Status)
		}
		var s session
		if err := json.NewDecoder(res.Body).Decode(&s); err != nil {
			panic(err)
		}
		return s
	}

	file, err := os.Open("big.iso")
	if err != nil {
		panic(err)
	}
	defer file.Close()
	info, _ := file.Stat()

	uploadID := os.Getenv("UPLOAD_ID") // set this to pick up an upload that stopped
	if uploadID == "" {
		start, _ := json.Marshal(map[string]any{"name": "big.iso", "size": info.Size()})
		uploadID = send("POST", "/api/v1/uploads", bytes.NewReader(start)).ID
	}
	fmt.Println("Upload", uploadID)

	// Ask where things really stand, because the account may have kept less than was sent.
	s := send("GET", "/api/v1/uploads/"+uploadID, nil)
	buffer := make([]byte, piece)
	for s.File == nil {
		n, err := file.ReadAt(buffer, s.Received)
		if err != nil && err != io.EOF {
			panic(err)
		}
		path := fmt.Sprintf("/api/v1/uploads/%s/chunk?offset=%d", uploadID, s.Received)
		s = send("PUT", path, bytes.NewReader(buffer[:n]))
	}
	fmt.Println(s.File.ID)
}
