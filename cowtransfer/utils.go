package cowtransfer

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

func buildMultipartBody(m map[string]string) (body *bytes.Buffer, boundary string, err error) {
	body = &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	boundary = writer.Boundary()

	for k, v := range m {
		err = writer.WriteField(k, v)
		if err != nil {
			return
		}
	}
	writer.Close()

	return
}

func valuesToMap(values url.Values) map[string]string {
	m := map[string]string{}
	for k, v := range values {
		if len(v) > 0 {
			m[k] = v[0]
		} else {
			m[k] = ""
		}
	}
	return m
}

func (c *Client) postForm(url string, body url.Values) (*http.Response, error) {
	b, boundary, err := buildMultipartBody(valuesToMap(body))
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", url, b)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	req.Header.Set("Host", "cowtransfer.com")
	req.Header.Set("Origin", "https://cowtransfer.com")
	req.Header.Set("Referer", "https://cowtransfer.com")
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/80.0.3987.149 Safari/537.36")
	req.Header.Set("Cookie", c.Cookie())
	req.Header.Set("Content-Length", strconv.Itoa(b.Len()))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	for _, cookie := range resp.Header.Values("Set-Cookie") {
		kv := strings.Split(cookie, ";")[0]
		entry := strings.Split(kv, "=")
		c.cookie[entry[0]] = entry[1]
	}
	return resp, nil
}

func detectContentType(filename string) (string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return "", err
	}
	defer file.Close()

	b := make([]byte, 512)
	_, err = file.Read(b)
	if err != nil {
		return "", err
	}

	return http.DetectContentType(b), nil
}

func (c *Client) Cookie() string {
	var cookies string
	var count int

	for k, v := range c.cookie {
		if count > 0 {
			cookies += ";"
		}
		cookies += k + "=" + v
		count++
	}
	return cookies
}
