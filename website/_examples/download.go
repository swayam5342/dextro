package main

import (
	"io"
	"net/http"
	"os"
)

func main() {
	base, key := os.Getenv("TRIBUTARY_URL"), os.Getenv("TRIBUTARY_KEY")

	req, _ := http.NewRequest("GET", base+"/api/v1/files/"+os.Getenv("FILE_ID")+"/content", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		panic(res.Status)
	}

	out, err := os.Create("report.pdf")
	if err != nil {
		panic(err)
	}
	defer out.Close()
	if _, err := io.Copy(out, res.Body); err != nil {
		panic(err)
	}
}
