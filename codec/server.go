package codec

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"reflect"
	"sync"
)

const SerialNum = 0xa3bc8e

type Option struct {
	SerialNum int
	CodecType Type
}

var DefaultOption = &Option{
	SerialNum: SerialNum,
	CodecType: GobType,
}

type Server struct{}

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

// Accept 接受监听器中的连接
func Accept(l net.Listener) {
	DefaultServer.Accept(l)
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
	f := NewCodecFuncMap[opt.CodecType]
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
func (s *Server) serveCodec(c Codec) {
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

// request包含所有消息信息
type request struct {
	h     *Header
	args  reflect.Value
	reply reflect.Value
}

func (s *Server) readRequestHeader(c Codec) (*Header, error) {
	var h Header
	if err := c.ReadHeader(&h); err != nil {
		if err != io.EOF && !errors.Is(err, io.ErrUnexpectedEOF) {
			log.Println("rpc server error(read header):", err)
		}
		return nil, err
	}
	return &h, nil
}

func (s *Server) readRequest(c Codec) (*request, error) {
	h, err := s.readRequestHeader(c)
	if err != nil {
		return nil, err
	}
	req := &request{h: h}
	req.args = reflect.New(reflect.TypeOf(""))
	if err = c.ReadBody(req.args.Interface()); err != nil {
		log.Println("rpc server error(read body):", err)
	}
	return req, nil
}

func (s *Server) sendResponse(c Codec, h *Header, body interface{}, locker *sync.Mutex) {
	locker.Lock()
	defer locker.Unlock()
	if err := c.Write(h, body); err != nil {
		log.Println("rpc server error(write body):", err)
	}
}

func (s *Server) handleRequest(c Codec, rq *request, locker *sync.Mutex, wg *sync.WaitGroup) {
	defer wg.Done()
	log.Println(rq.h, rq.args.Elem())
	rq.reply = reflect.ValueOf(fmt.Sprintf("rpc resp %d", rq.h.Seq))
	s.sendResponse(c, rq.h, rq.reply.Interface(), locker)
}
