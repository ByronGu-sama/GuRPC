package main

import (
	"GuRPC/codec"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"reflect"
	"strings"
	"sync"
)

const SerialNum = 0xa3bc8e

type Option struct {
	SerialNum int
	CodecType codec.Type
}

var DefaultOption = &Option{
	SerialNum: SerialNum,
	CodecType: codec.GobType,
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
	s.serveCodec(f(conn))
}

// 响应体占位符
var invalidRequest = struct{}{}

// 处理请求
// 并发处理请求
// 按顺序发送请求，因为并发容易导致多个报文混乱，客户端无法解析
func (s *Server) serveCodec(c codec.Codec) {
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
		go s.handleRequest(c, req, locker, wg)
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

func (server *Server) handleRequest(c codec.Codec, req *request, locker *sync.Mutex, wg *sync.WaitGroup) {
	defer wg.Done()
	err := req.sv.call(req.mType, req.arg, req.reply)
	if err != nil {
		req.h.Error = err.Error()
		server.sendResponse(c, req.h, invalidRequest, locker)
	}
	server.sendResponse(c, req.h, req.reply.Interface(), locker)
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
