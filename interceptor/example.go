package interceptor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"

	"github.com/ouqiang/goproxy"
)

func init() {
	Handler = new(Example)
}

type Example struct{}

// Connect 收到客户端连接, 自定义response返回
// NOTICE: HTTPS只能访问 ctx.Req.URL.Host, 不能访问Header和Body, 不能使用rw
func (Example) Connect(ctx *goproxy.Context, rw http.ResponseWriter) {
}

// BeforeRequest 请求发送前, 修改request
func (Example) BeforeRequest(ctx *goproxy.Context) {
	// ctx.Abort() 阻塞API调用
}

// BeforeResponse 响应发送前, 修改response
func (Example) BeforeResponse(ctx *goproxy.Context, resp *http.Response, err error) {
	modifyRespIfNeeded(ctx, resp)
}

func modifyRespIfNeeded(ctx *goproxy.Context, resp *http.Response) {
	if strings.HasPrefix(ctx.Req.URL.Host, "xmnup-rxe-1-api.lab.nordigy.ru") && strings.HasPrefix(ctx.Req.URL.Path, "/restapi/v1.0/rooms-client/account") {
		contentType := getContentType(resp.Header)

		if strings.HasPrefix(contentType, "application/json") {
			body, err := ioutil.ReadAll(resp.Body)

			if err == nil {
				retBody, modified := modifyBodyIfNeeded(body)
				if modified {
					fmt.Println("replace body with modified data")
					resp.ContentLength = int64(len(retBody))

					// Replace with modified body
					resp.Body = ioutil.NopCloser(bytes.NewReader(retBody))
				}
			}
		}
	}
}

// Parse the original body and modify it, if it is needed
func modifyBodyIfNeeded(body []byte) ([]byte, bool) {
	var jsonObj interface{}

	err := json.Unmarshal(body, &jsonObj)

	if err != nil {
		fmt.Println("Can not ready json body")
		return body, false
	}

	records, ok := jsonObj.(map[string]interface{})["records"]

	hasModified := false

	if ok {
		for _, record := range records.([]interface{}) {
			recordMap, ok := record.(map[string]interface{})

			if ok {
				key, ok := recordMap["settingId"].(string)
				if ok {
					if strings.HasPrefix(key, "DigitalSignage.PlayEnabled") {
						recordMap["value"] = "true"
						hasModified = true
					}
				}
			}
		}
	}

	if hasModified {
		ret, err := json.Marshal(jsonObj)

		if err == nil {
			return ret, true
		}
	}

	return body, false
}

func getContentType(h http.Header) string {
	ct := h.Get("Content-Type")
	segments := strings.Split(strings.TrimSpace(ct), ";")
	if len(segments) > 0 && segments[0] != "" {
		return strings.TrimSpace(segments[0])
	}

	return "application/octet-stream" //content type binary
}
