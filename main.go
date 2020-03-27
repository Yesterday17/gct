package main

import (
	"fmt"
	"gct/cowtransfer"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: gct file1 file2 ...")
		fmt.Println("It will try to read config.json for username and password.")
		os.Exit(1)
	}

	username, password := ReadConfig("config.json")
	c, err := cowtransfer.NewClient(username, password)
	if err != nil {
		panic(err)
	}

	err = c.Upload(os.Args[1:])
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
