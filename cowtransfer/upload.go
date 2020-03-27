package cowtransfer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/url"
	"os"
	"path"
	"strconv"
)

type prepareSendResponse struct {
	Uptoken      string `json:"uptoken"`
	TransferGUID string `json:"transferguid"`
	UniqueUrl    string `json:"uniqueurl"`
	Prefix       string `json:"prefix"`

	Error        bool   `json:"error"`
	ErrorMessage string `json:"error_message"`
}

func (c *Client) prepareSend(files []string) (*prepareSendResponse, error) {
	var totalSize int64

	for _, f := range files {
		info, err := os.Stat(f)
		if err != nil {
			return nil, err
		}
		totalSize += info.Size()
	}

	resp, err := c.postForm("https://cowtransfer.com/transfer/preparesend", url.Values{
		"totalSize":           []string{strconv.FormatInt(totalSize, 10)},
		"message":             []string{},
		"notifyEmail":         []string{},
		"validDays":           []string{"7"},
		"sendToMyCloud":       []string{},
		"downloadTimes":       []string{"-1"},
		"smsReceivers":        []string{},
		"emailReceivers":      []string{},
		"enableShareToOthers": []string{"false"},
	})
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var r prepareSendResponse
	err = json.Unmarshal(body, &r)
	if err != nil {
		return nil, err
	}

	if r.Error {
		return nil, errors.New(r.ErrorMessage)
	}

	return &r, nil
}

type beforeUploadResponse struct {
	ExpireAt string `json:"expireAt"` // Hours
}

func (c *Client) beforeUpload(file string, size int64, prepare *prepareSendResponse) (string, error) {
	contentType, err := detectContentType(file)
	if err != nil {
		return "", err
	}

	resp, err := c.postForm("https://cowtransfer.com/transfer/beforeupload", url.Values{
		"type":          []string{contentType},
		"fileId":        []string{},
		"fileName":      []string{path.Base(file)},
		"fileSize":      []string{strconv.FormatInt(size, 10)},
		"transferGuid":  []string{prepare.TransferGUID},
		"storagePrefix": []string{prepare.Prefix},
	})
	if err != nil {
		return "", err
	}

	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var r beforeUploadResponse
	err = json.Unmarshal(body, &r)
	if err != nil {
		return "", err
	}

	return r.ExpireAt, nil
}

func (c *Client) upload(file string, prepare *prepareSendResponse, size int64, log chan string) error {
	var current int64
	prog := make([]blockPutReturn, blockCount(size))
	err := putFile(
		context.Background(),
		prepare.Uptoken,
		prepare.Prefix+"/"+prepare.TransferGUID+"/"+path.Base(file),
		file,
		&rPutExtra{
			Progresses: prog,
			Notify: func(blkIdx int, blkSize int, ret *blockPutReturn) {
				current += int64(blkSize)
				prog[blkIdx] = *ret
				log <- fmt.Sprintf("[%d%%] Block %d written", current*100/size, blkIdx)
			},
			NotifyErr: func(blkIdx int, blkSize int, err error) {
				log <- fmt.Sprintf("Failed to write write block %d of %s: %v\n", blkIdx, path.Base(file), err)
			},
		},
	)
	if err != nil {
		return err
	}

	return nil
}

func (c *Client) uploaded(guid string) (bool, error) {
	resp, err := c.postForm("https://cowtransfer.com/transfer/uploaded", url.Values{
		"fileId":       []string{},
		"transferGuid": []string{guid},
	})
	if err != nil {
		return false, err
	}

	r, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}

	return string(r) == "true", nil
}

type completeResponse struct {
	TempDownloadCode string `json:"tempDownloadCode"`
	Complete         bool   `json:"complete"`
}

func (c *Client) complete(guid string) (string, error) {
	resp, err := c.postForm("https://cowtransfer.com/transfer/complete", url.Values{
		"transferGuid": []string{guid},
	})
	if err != nil {
		return "", err
	}

	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var r completeResponse
	err = json.Unmarshal(body, &r)
	if err != nil {
		return "", err
	}

	return r.TempDownloadCode, nil
}

func (c *Client) Upload(files []string, log chan string) error {
	prepare, err := c.prepareSend(files)
	if err != nil {
		return err
	}
	log <- fmt.Sprintf("UniqueUrl: %s", prepare.UniqueUrl)

	for i, file := range files {
		info, err := os.Stat(file)
		if err != nil {
			return err
		}

		expire, err := c.beforeUpload(file, info.Size(), prepare)
		if err != nil {
			return err
		}
		log <- fmt.Sprintf("[File #%d] Expire: %s hours", i+1, expire)

		log <- fmt.Sprintf("[File #%d] Uploading %s...", i+1, info.Name())
		err = c.upload(file, prepare, info.Size(), log)
		if err != nil {
			return err
		}

		ok, err := c.uploaded(prepare.TransferGUID)
		if err != nil {
			return err
		}
		log <- fmt.Sprintf("[File #%d] %s Ok: %v", i+1, info.Name(), ok)
	}

	code, err := c.complete(prepare.TransferGUID)
	if err != nil {
		return err
	}
	fmt.Printf("Share code: %s", code)

	return nil
}
