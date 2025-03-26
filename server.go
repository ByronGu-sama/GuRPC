package main

import (
	"GuRPC/codec"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"
)

const SerialNum = 0xa3bc8e

type Option struct {
	SerialNum      int
	CodecType      codec.Type
	ConnectTimeout time.Duration
	HandleTimeout  time.Duration
}

var DefaultOption = &Option{
	SerialNum:      SerialNum,
	CodecType:      codec.GobType,
	ConnectTimeout: time.Second * 10,
}

type Server struct {
	serviceMap sync.Map
}

// NewServer 新建Server对象
func NewServer() *Server {
	return &Server{}
}

var DefaultServer = NewServer()

// Accept 接受监听器中的连接
func (s *Server) Accept(l net.Listener) {
	for {
		conn, err := l.Accept()
		if err != nil {
			log.Println("rpc server error: accept", err)
			return
		}
		go s.ServeConn(conn)
	}
}

func (s *Server) ServeConn(conn io.ReadWriteCloser) {
	defer func() {
		_ = conn.Close()
	}()
	var opt Option
	// 解码
	if err := json.NewDecoder(conn).Decode(&opt); err != nil {
		log.Println("rpc server error(option)", err)
		return
	}
	// 校验serialNum
	if opt.SerialNum != SerialNum {
		log.Println("rpc server error(option)", "serial num not match:", opt.SerialNum)
		return
	}
	f := codec.NewCodecFuncMap[opt.CodecType]
	if f == nil {
		log.Println("rpc server error(option)", opt.CodecType)
		return
	}
	s.serveCodec(f(conn), &opt)
}

// 响应体占位符
var invalidRequest = struct{}{}

// 处理请求
// 并发处理请求
// 按顺序发送请求，因为并发容易导致多个报文混乱，客户端无法解析
func (s *Server) serveCodec(c codec.Codec, opt *Option) {
	locker := new(sync.Mutex)
	wg := &sync.WaitGroup{}
	for {
		req, err := s.readRequest(c)
		if err != nil {
			if req == nil {
				break
			}
			req.h.Error = err.Error()
			s.sendResponse(c, req.h, invalidRequest, locker)
			continue
		}
		wg.Add(1)
		go s.handleRequest(c, req, locker, wg, opt.HandleTimeout)
	}
	wg.Wait()
	_ = c.Close()
}

type request struct {
	h     *codec.Header
	arg   reflect.Value
	reply reflect.Value
	mType *methodType
	sv    *service
}

// request包含所有消息信息
func (s *Server) readRequestHeader(c codec.Codec) (*codec.Header, error) {
	var h codec.Header
	if err := c.ReadHeader(&h); err != nil {
		if err != io.EOF && !errors.Is(err, io.ErrUnexpectedEOF) {
			log.Println("rpc server error(read header):", err)
		}
		return nil, err
	}
	return &h, nil
}

// 发现服务
func (server *Server) findService(serviceMethod string) (svc *service, mtype *methodType, err error) {
	dot := strings.LastIndex(serviceMethod, ".")
	if dot < 0 {
		err = errors.New("rpc server: service/method request ill-formed: " + serviceMethod)
		return
	}
	serviceName, methodName := serviceMethod[:dot], serviceMethod[dot+1:]
	svci, ok := server.serviceMap.Load(serviceName)
	if !ok {
		err = errors.New("rpc server: can't find service " + serviceName)
		return
	}
	svc = svci.(*service)
	mtype = svc.method[methodName]
	if mtype == nil {
		err = errors.New("rpc server: can't find method " + methodName)
	}
	return
}

// 读取请求
func (s *Server) readRequest(c codec.Codec) (*request, error) {
	h, err := s.readRequestHeader(c)
	if err != nil {
		return nil, err
	}
	req := &request{h: h}
	req.sv, req.mType, err = s.findService(h.ServiceMethod)
	if err != nil {
		return req, err
	}
	req.arg = req.mType.newArgs()
	req.reply = req.mType.newReply()

	argi := req.arg.Interface()
	if req.arg.Type().Kind() != reflect.Ptr {
		argi = req.arg.Addr().Interface()
	}
	if err = c.ReadBody(argi); err != nil {
		log.Println("rpc server: can't read body:", err)
		return req, err
	}
	return req, nil
}

func (s *Server) sendResponse(c codec.Codec, h *codec.Header, body interface{}, locker *sync.Mutex) {
	locker.Lock()
	defer locker.Unlock()
	if err := c.Write(h, body); err != nil {
		log.Println("rpc server error(write body):", err)
	}
}

func (server *Server) handleRequest(c codec.Codec, req *request, locker *sync.Mutex, wg *sync.WaitGroup, timeout time.Duration) {
	defer wg.Done()
	called := make(chan struct{})
	sent := make(chan struct{})

	go func() {
		err := req.sv.call(req.mType, req.arg, req.reply)
		called <- struct{}{}
		if err != nil {
			req.h.Error = err.Error()
			server.sendResponse(c, req.h, req.reply.Interface(), locker)
			sent <- struct{}{}
			return
		}
		server.sendResponse(c, req.h, req.reply.Interface(), locker)
		sent <- struct{}{}
	}()

	if timeout == 0 {
		<-called
		<-sent
		return
	}

	select {
	case <-time.After(timeout):
		req.h.Error = "rpc server: timeout"
		server.sendResponse(c, req.h, req.reply.Interface(), locker)
	case <-called:
		<-sent
	}
}

func Accept(lis net.Listener) { DefaultServer.Accept(lis) }

func (server *Server) Register(rcv interface{}) error {
	s := newService(rcv)
	if _, dup := server.serviceMap.LoadOrStore(s.name, s); dup {
		return errors.New("rpc: service already defined: " + s.name)
	}
	return nil
}

func Register(rcvr interface{}) error { return DefaultServer.Register(rcvr) }

const (
	connected        = "200 connected to Gu RPC"
	defaultRPCPath   = "/_gurpc_"
	defaultDebugPath = "/debug/gurpc"
)

func (s *Server) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	// 如果不是CONNECT请求，则拒绝请求
	if req.Method != "CONNECT" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = io.WriteString(w, "405 must CONNECT\n")
		return
	}

	// 劫持http连接
	conn, _, err := w.(http.Hijacker).Hijack()
	if err != nil {
		log.Println("rpc hijacking", req.RemoteAddr, ": ", err.Error())
		return
	}
	// 写入请求头，表明连接成功
	_, _ = io.WriteString(conn, "HTTP/1.0 "+connected+"\n\n")
	s.ServeConn(conn)
}

// HandleHTTP 注册http处理器
func (s *Server) HandleHTTP() {
	http.Handle(defaultRPCPath, s)
	http.Handle(defaultDebugPath, debugHTTP{s})
	log.Println("rpc server debug path:", defaultDebugPath)
}

// HandleHTTP 默认的http服务注册器
func HandleHTTP() {
	DefaultServer.HandleHTTP()
}
