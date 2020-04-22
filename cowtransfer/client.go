package cowtransfer

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	cookie map[string]string
}

func NewClient(username, password string) (*Client, error) {
	c := &Client{
		cookie: map[string]string{},
	}
	c.cookie["cf-cs-k-20181214"] = strconv.FormatInt(time.Now().Unix(), 10)
	resp, err := http.PostForm("https://cowtransfer.com/user/emaillogin", url.Values{
		"email":    []string{username},
		"password": []string{password},
	})
	if err != nil {
		return nil, err
	}

	kv := strings.Split(resp.Header.Get("Set-Cookie"), ";")[0]
	entry := strings.Split(kv, "=")
	c.cookie[entry[0]] = entry[1]
	fmt.Println("Cookie: " + c.Cookie())

	return c, nil
}
