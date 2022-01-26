package recorder

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"time"

	"github.com/ouqiang/goproxy"
	log "github.com/sirupsen/logrus"
)

// Storage 存取transaction接口
type Storage interface {
	Get(txId string) (*Transaction, error)
	Put(*Transaction) error
}

// Output 输出transaction接口
type Output interface {
	Write(*Transaction) error
}

// Interceptor 拦截器
type Interceptor interface {
	// Connect 收到客户端连接, 自定义response返回, 只支持HTTP
	Connect(ctx *goproxy.Context, rw http.ResponseWriter)
	// BeforeRequest 请求发送前, 修改request
	BeforeRequest(ctx *goproxy.Context)
	// BeforeResponse 响应发送前, 修改response
	BeforeResponse(ctx *goproxy.Context, resp *http.Response, err error)
}

// Recorder 记录http transaction
type Recorder struct {
	proxy       *goproxy.Proxy
	storage     Storage
	output      Output
	interceptor Interceptor
}

// NewRecorder 创建recorder
func NewRecorder() *Recorder {
	r := &Recorder{}

	return r
}

// SetProxy 设置中间人代理
func (r *Recorder) SetProxy(p *goproxy.Proxy) {
	r.proxy = p
}

// SetStorage 设置transaction存储
func (r *Recorder) SetStorage(s Storage) {
	r.storage = s
}

// SetOutput 设置transaction输出
func (r *Recorder) SetOutput(o Output) {
	r.output = o
}

// SetInterceptor 设置拦截器
func (r *Recorder) SetInterceptor(i Interceptor) {
	r.interceptor = i
}

// Storage 获取存储
func (r *Recorder) Storage() Storage {
	return r.storage
}

// Connect 收到客户端连接
func (r *Recorder) Connect(ctx *goproxy.Context, rw http.ResponseWriter) {
	if r.interceptor != nil {
		r.interceptor.Connect(ctx, rw)
	}
}

// Auth 代理身份认证
func (r *Recorder) Auth(ctx *goproxy.Context, rw http.ResponseWriter) {}

// BeforeRequest 请求发送前处理
func (r *Recorder) BeforeRequest(ctx *goproxy.Context) {
	if host := ctx.Req.Header.Get("X-Mars-Host"); host != "" {
		ctx.Req.Host = host
	}
	ctx.Req.Header.Del("X-Mars-Host")
	ctx.Req.Header.Del("X-Mars-Debug")
	if r.interceptor != nil {
		r.interceptor.BeforeRequest(ctx)
	}
	tx := NewTransaction()
	tx.ClientIP, _, _ = net.SplitHostPort(ctx.Req.RemoteAddr)
	tx.StartTime = time.Now()

	tx.DumpRequest(ctx.Req)

	trace := &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) {
			tx.ServerIP, _, _ = net.SplitHostPort(info.Conn.RemoteAddr().String())
		},
	}
	ctx.Req = ctx.Req.WithContext(httptrace.WithClientTrace(ctx.Req.Context(), trace))

	ctx.Data["tx"] = tx
}

func (r *Recorder) RewriteResp(ctx *goproxy.Context, resp *http.Response) {
	if strings.HasPrefix(ctx.Req.URL.Host, "xmnup-rxe-1-api.lab.nordigy.ru") && strings.HasPrefix(ctx.Req.URL.Path, "/restapi/v1.0/rooms-client/account") {
		contentType := getContentType(resp.Header)

		if strings.HasPrefix(contentType, "application/json") {
			body, err := ioutil.ReadAll(resp.Body)

			if err == nil {
				rebody, update := r.RewriteBody(body)
				if update {
					resp.ContentLength = int64(len(rebody))
					resp.Body = ioutil.NopCloser(bytes.NewReader(rebody))
				}
			}
		}
	}
}

func (r *Recorder) RewriteBody(body []byte) ([]byte, bool) {
	var jsonObj interface{}

	err := json.Unmarshal(body, &jsonObj)

	if err != nil {
		fmt.Println("Can not ready json body")
		return body, false
	}

	records, ok := jsonObj.(map[string]interface{})["records"]

	hasUpdate := false

	if ok {
		for _, record := range records.([]interface{}) {
			recordMap, ok := record.(map[string]interface{})

			if ok {
				key, ok := recordMap["settingId"].(string)
				if ok {
					if strings.HasPrefix(key, "DigitalSignage.PlayEnabled") {
						recordMap["value"] = "true"
						hasUpdate = true
					}
				}
			}
		}
	}

	if hasUpdate {
		ret, err := json.Marshal(jsonObj)

		if err == nil {
			fmt.Println("rewrite body success")
			return ret, true
		}
	}

	return body, false
}

// BeforeResponse 响应发送前处理
func (r *Recorder) BeforeResponse(ctx *goproxy.Context, resp *http.Response, err error) {
	if r.interceptor != nil {
		r.interceptor.BeforeResponse(ctx, resp, err)
	}
	tx := ctx.Data["tx"].(*Transaction)
	tx.Duration = time.Now().Sub(tx.StartTime)

	r.RewriteResp(ctx, resp)

	tx.DumpResponse(resp, err)
}

// ParentProxy 设置上级代理
func (r *Recorder) ParentProxy(req *http.Request) (*url.URL, error) {
	return http.ProxyFromEnvironment(req)
}

// Finish 请求结束
func (r *Recorder) Finish(ctx *goproxy.Context) {
	value, ok := ctx.Data["tx"]
	if !ok {
		return
	}
	tx, ok := value.(*Transaction)
	if !ok {
		return
	}
	if r.storage != nil {
		err := r.storage.Put(tx)
		if err != nil {
			log.Warnf("请求结束#保存transaction错误: [%s] %s", ctx.Req.URL.String(), err)
		}
	}
	if r.output != nil {
		err := r.output.Write(tx)
		if err != nil {
			log.Warnf("请求结束#输出transaction错误: [%s] %s",
				ctx.Req.URL.String(), err)
		}
	}
}

// ErrorLog 记录错误日志
func (r *Recorder) ErrorLog(err error) {
	log.Error(err)
}

// Replay 回放
func (r *Recorder) Replay(txId string) error {
	tx, err := r.storage.Get(txId)
	if err != nil {
		return fmt.Errorf("回放#获取transaction错误: [txId: %s] %s", txId, err)
	}
	newReq, err := tx.Req.Restore()
	if err != nil {
		return fmt.Errorf("回放#创建请求错误: [txId: %s] %s", txId, err)
	}
	newReq.RemoteAddr = tx.ClientIP + ":80"
	go r.DoRequest(newReq)

	return nil
}

// DoRequest 执行请求
func (r *Recorder) DoRequest(req *http.Request) {
	if req == nil {
		panic("request is nil")
	}
	ctx := &goproxy.Context{
		Req: req,
	}
	r.proxy.DoRequest(ctx, func(resp *http.Response, e error) {
		if resp != nil {
			resp.Body.Close()
		}
	})
	r.Finish(ctx)
}
