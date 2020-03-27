package cowtransfer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/qiniu/api.v7/v7/storage"
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

func (c *Client) upload(file string, prepare *prepareSendResponse, size int64) error {
	var uploader *storage.ResumeUploader
	uploader = storage.NewResumeUploader(&storage.Config{
		Zone:          &storage.ZoneHuadong,
		UseHTTPS:      true,
		UseCdnDomains: true,
	})

	var current int64
	prog := make([]storage.BlkputRet, storage.BlockCount(size))
	ret := storage.PutRet{}
	err := uploader.PutFile(
		context.Background(),
		&ret,
		prepare.Uptoken,
		prepare.Prefix+"/"+prepare.TransferGUID+"/"+path.Base(file),
		file,
		&storage.RputExtra{
			Progresses: prog,
			Notify: func(blkIdx int, blkSize int, ret *storage.BlkputRet) {
				current += int64(blkSize)
				prog[blkIdx] = *ret
				fmt.Printf("[%d%%] Block %d written\n", current*100/size, blkIdx)
			},
			NotifyErr: func(blkIdx int, blkSize int, err error) {
				fmt.Printf("Failed to write write block %d of %s: %v\n", blkIdx, path.Base(file), err)
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

func (c *Client) Upload(files []string) error {
	prepare, err := c.prepareSend(files)
	if err != nil {
		return err
	}
	fmt.Printf("UniqueUrl: %s\n", prepare.UniqueUrl)

	for i, file := range files {
		info, err := os.Stat(file)
		if err != nil {
			return err
		}

		expire, err := c.beforeUpload(file, info.Size(), prepare)
		if err != nil {
			return err
		}
		fmt.Printf("[File #%d] Expire: %s hours\n", i+1, expire)

		fmt.Printf("[File #%d] Uploading %s...\n", i+1, info.Name())
		err = c.upload(file, prepare, info.Size())
		if err != nil {
			return err
		}

		ok, err := c.uploaded(prepare.TransferGUID)
		if err != nil {
			return err
		}
		fmt.Printf("[File #%d] %s Ok: %v\n", i+1, info.Name(), ok)
	}

	code, err := c.complete(prepare.TransferGUID)
	if err != nil {
		return err
	}
	fmt.Printf("Share code: %s", code)

	return nil
}
