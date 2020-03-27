package cowtransfer

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"hash/crc32"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync"
)

type Uploader struct {
}

var (
	ErrUnmatchedChecksum = errors.New("unmatched checksum")
)

const (
	defaultWorkers       = 4               // 默认的并发上传的块数量
	defaultChunkSize     = 4 * 1024 * 1024 // 默认的分片大小，4MB
	defaultTryTimes      = 3               // bput 失败重试次数
	defaultTaskQueueSize = 16              // 任务队列大小
	uploadHost           = "https://upload.qiniup.com"
)

var tasks chan func()

func worker(tasks chan func()) {
	for {
		task := <-tasks
		task()
	}
}

func initWorkers() {
	tasks = make(chan func(), defaultTaskQueueSize)
	for i := 0; i < defaultWorkers; i++ {
		go worker(tasks)
	}
}

const (
	blockBits = 22
	blockMask = (1 << blockBits) - 1
)

// blockCount 用来计算文件的分块数量
func blockCount(fsize int64) int {
	return int((fsize + blockMask) >> blockBits)
}

// blockPutReturn 表示分片上传每个片上传完毕的返回值
type blockPutReturn struct {
	Ctx       string `json:"ctx"`
	Checksum  string `json:"checksum"`
	Crc32     uint32 `json:"crc32"`
	Offset    uint32 `json:"offset"`
	Host      string `json:"host"`
	ExpiredAt int64  `json:"expired_at"`
}

// rPutExtra 表示分片上传额外可以指定的参数
type rPutExtra struct {
	Progresses []blockPutReturn                                   // 可选。上传进度
	Notify     func(blkIdx int, blkSize int, ret *blockPutReturn) // 可选。进度提示（注意多个block是并行传输的）
	NotifyErr  func(blkIdx int, blkSize int, err error)
}

var once sync.Once

func putFile(ctx context.Context, upToken, key, localFile string, extra *rPutExtra) error {
	// Init workers once
	once.Do(initWorkers)

	f, err := os.Open(localFile)
	if err != nil {
		return err
	}
	defer f.Close()

	fs, err := f.Stat()
	if err != nil {
		return err
	}
	fsize := fs.Size()

	blockCount := blockCount(fsize)
	lastBlockIndex := blockCount - 1

	var wg sync.WaitGroup
	wg.Add(blockCount)

	blockSize := 1 << blockBits
	nfails := 0

	for i := 0; i < blockCount; i++ {
		currentBlockId := i
		currentBlockSize := blockSize
		if i == lastBlockIndex {
			offbase := int64(currentBlockId) << blockBits
			currentBlockSize = int(fsize - offbase)
		}

		tasks <- func() {
			defer wg.Done()
			for tryTimeslast := defaultTryTimes; tryTimeslast > 1; tryTimeslast-- {
				err := resumableBput(ctx, upToken, &extra.Progresses[currentBlockId], f, currentBlockId, currentBlockSize, extra)
				if err == nil {
					break
				} else if tryTimeslast == 1 {
					extra.NotifyErr(currentBlockId, currentBlockSize, err)
					nfails++
				}
			}
		}
	}

	wg.Wait()
	if nfails != 0 {
		return errors.New("resumable put failed")
	}

	return mkFile(ctx, upToken, key, fsize, extra)
}

func encode(raw string) string {
	return base64.URLEncoding.EncodeToString([]byte(raw))
}

// 分片上传请求
func resumableBput(
	ctx context.Context,
	upToken string,
	ret *blockPutReturn,
	f io.ReaderAt,
	blockIdx, blockSize int,
	extra *rPutExtra) (err error) {

	h := crc32.NewIEEE()
	offbase := int64(blockIdx) << blockBits
	chunkSize := defaultChunkSize

	var bodyLength int

	if ret.Ctx == "" {

		if chunkSize < blockSize {
			bodyLength = chunkSize
		} else {
			bodyLength = blockSize
		}

		body1 := io.NewSectionReader(f, offbase, int64(bodyLength))
		body := io.TeeReader(body1, h)

		err = mkblk(ctx, upToken, ret, blockSize, body, bodyLength)
		if err != nil {
			return
		}
		if ret.Crc32 != h.Sum32() || int(ret.Offset) != bodyLength {
			err = ErrUnmatchedChecksum
			return
		}
		extra.Notify(blockIdx, blockSize, ret)
	}

	for int(ret.Offset) < blockSize {

		if chunkSize < blockSize-int(ret.Offset) {
			bodyLength = chunkSize
		} else {
			bodyLength = blockSize - int(ret.Offset)
		}

		tryTimes := defaultTryTimes

	lzRetry:
		h.Reset()
		body1 := io.NewSectionReader(f, offbase+int64(ret.Offset), int64(bodyLength))
		body := io.TeeReader(body1, h)

		err = bPut(ctx, upToken, ret, body, bodyLength)
		if err == nil {
			if ret.Crc32 == h.Sum32() {
				extra.Notify(blockIdx, blockSize, ret)
				continue
			}
			err = ErrUnmatchedChecksum
		} else {
			return
		}
		if tryTimes > 1 {
			tryTimes--
			goto lzRetry
		}
		break
	}
	return
}

// Make block
func mkblk(ctx context.Context, upToken string, ret *blockPutReturn, blockSize int, body io.Reader, size int) error {
	url := uploadHost + "/mkblk/" + strconv.Itoa(blockSize)
	return uploadPost(ctx, url, upToken, body, size, ret)
}

// Merge blocks to a file
func mkFile(ctx context.Context, upToken string, key string, fsize int64, extra *rPutExtra) error {
	url := uploadHost + "/mkfile/" + strconv.FormatInt(fsize, 10) + "/key/" + encode(key)

	buf := make([]byte, 0, 196*len(extra.Progresses))
	for _, pr := range extra.Progresses {
		buf = append(buf, pr.Ctx...)
		buf = append(buf, ',')
	}

	if len(buf) > 0 {
		buf = buf[:len(buf)-1]
	}

	return uploadPost(ctx, url, upToken, bytes.NewReader(buf), len(buf), nil)
}

func bPut(ctx context.Context, upToken string, ret *blockPutReturn, body io.Reader, size int) error {
	url := ret.Host + "/bput/" + ret.Ctx + "/" + strconv.FormatUint(uint64(ret.Offset), 10)
	return uploadPost(ctx, url, upToken, body, size, ret)
}

func uploadPost(ctx context.Context, url, upToken string, body io.Reader, size int, ret interface{}) error {
	headers := http.Header{}
	headers.Add("Content-Type", "application/octet-stream")
	headers.Add("Authorization", "UpToken "+upToken)

	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		return err
	}

	req.WithContext(ctx)
	req.Header = headers
	req.ContentLength = int64(size)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}

	if resp.StatusCode/100 != 2 {
		return errors.New("Invalid response status")
	}

	if ret != nil && resp.ContentLength != 0 {
		return json.NewDecoder(resp.Body).Decode(ret)
	}
	if resp.StatusCode == 200 {
		return nil
	}
	return nil
}
