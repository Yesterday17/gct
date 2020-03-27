package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
)

func ReadConfig(file string) (string, string) {
	data, err := ioutil.ReadFile(file)
	if err != nil {
		fmt.Printf("Failed to read file: %s, fallback to anonymous.\n", file)
		return "", ""
	}
	var config struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}

	err = json.Unmarshal(data, &config)
	if err != nil {
		fmt.Printf("Failed to parse config file: %s, fallback to anonymous.\n", file)
		return "", ""
	}

	return config.Username, config.Password
}
