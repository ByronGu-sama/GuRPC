package client

import (
	"GuRPC/codec"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
)

type Call struct {
	Seq           uint64
	ServiceMethod string      // service.method
	Args          interface{} // 函数参数
	Reply         interface{} // 函数响应
	Error         error
	Done          chan *Call
}

func (c *Call) done() {
	c.Done <- c
}

// Client RPC-Client 一个客户端可能同时被多个协程使用
type Client struct {
	c        codec.Codec
	opt      *codec.Option
	locker   sync.Mutex
	header   codec.Header
	mu       sync.Mutex
	seq      uint64
	pending  map[uint64]*Call
	closing  bool
	shutdown bool
}

var _ io.Closer = (*Client)(nil)

var ErrShutDown = errors.New("connection is shut down")

// Close 关闭客户端
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing {
		return ErrShutDown
	}
	c.closing = true
	return c.c.Close()
}

func (c *Client) IsAvailable() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.shutdown && !c.closing
}

// 注册消息，添加call至client.pending
func (c *Client) registerCall(call *Call) (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing && c.shutdown {
		return 0, ErrShutDown
	}
	call.Seq = c.seq
	c.pending[call.Seq] = call
	c.seq++
	return call.Seq, nil
}

// 根据seq删除call
func (c *Client) removeCall(seq uint64) *Call {
	c.mu.Lock()
	defer c.mu.Unlock()
	call := c.pending[seq]
	delete(c.pending, seq)
	return call
}

// 服务端或客户端发生错误时调用
func (c *Client) terminateCalls(err error) {
	c.locker.Lock()
	defer c.locker.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.shutdown = true
	for _, call := range c.pending {
		call.Error = err
		call.done()
	}
}

// 接收请求
func (c *Client) receive() {
	var err error
	for err == nil {
		var h codec.Header
		if err = c.c.ReadHeader(&h); err != nil {
			break
		}
		call := c.removeCall(h.Seq)
		switch {
		// call 不存在，可能是请求没有发送完整，或者因为其他原因被取消，但是服务端仍旧处理了。
		case call == nil:
			err = c.c.ReadBody(nil)
		// call 存在，但服务端处理出错，即 h.Error 不为空。
		case h.Error != "":
			call.Error = errors.New(h.Error)
			err = c.c.ReadBody(nil)
			call.done()
		//call 存在，服务端处理正常，那么需要从 body 中读取 Reply 的值。
		default:
			err = c.c.ReadBody(call.Reply)
			if err != nil {
				call.Error = errors.New("reading body: " + err.Error())
			}
			call.done()
		}
	}
	c.terminateCalls(err)
}

func NewClient(conn net.Conn, opt *codec.Option) (*Client, error) {
	f := codec.NewCodecFuncMap[opt.CodecType]
	if f == nil {
		err := fmt.Errorf("invalid codec type %s", opt.CodecType)
		log.Println("rpc client: codec error:", err)
		return nil, err
	}

	if err := json.NewEncoder(conn).Encode(opt); err != nil {
		log.Println("rpc client: options error:", err)
		_ = conn.Close()
		return nil, err
	}
	return newClientCodec(f(conn), opt), nil
}

// 创建客户端编解码器
func newClientCodec(c codec.Codec, opt *codec.Option) *Client {
	client := &Client{
		seq:     1, // 1有效，0无效
		c:       c,
		opt:     opt,
		pending: make(map[uint64]*Call),
	}
	go client.receive()
	return client
}

func parseOptions(opts ...*codec.Option) (*codec.Option, error) {
	// 使用默认配置
	if len(opts) == 0 || opts[0] == nil {
		return codec.DefaultOption, nil
	}

	if len(opts) != 1 {
		return nil, errors.New("invalid options")
	}

	opt := opts[0]
	opt.SerialNum = codec.DefaultOption.SerialNum
	if opt.CodecType == "" {
		opt.CodecType = codec.DefaultOption.CodecType
	}
	return opt, nil
}

func Dial(network, addr string, opts ...*codec.Option) (c *Client, err error) {
	opt, err := parseOptions(opts...)
	if err != nil {
		return nil, err
	}

	conn, err := net.Dial(network, addr)
	if err != nil {
		return nil, err
	}
	defer func() {
		if c == nil {
			_ = conn.Close()
		}
	}()
	return NewClient(conn, opt)
}

func (c *Client) send(call *Call) {
	// 加锁保证能发送完整消息
	c.locker.Lock()
	defer c.locker.Unlock()

	// 注册消息
	seq, err := c.registerCall(call)
	if err != nil {
		call.Error = err
		call.done()
		return
	}

	// 写入请求头
	c.header.ServiceMethod = call.ServiceMethod
	c.header.Seq = seq
	c.header.Error = ""

	// 编码后发送消息
	if err = c.c.Write(&c.header, call.Args); err != nil {
		call = c.removeCall(seq)
		if call != nil {
			call.Error = err
			call.done()
		}
	}
}

// Go 带锁激活函数
func (c *Client) Go(serviceMethod string, args, reply interface{}, done chan *Call) *Call {
	if done == nil {
		done = make(chan *Call, 10)
	} else if cap(done) == 0 {
		log.Panic("rpc client: done channel is unbuffered")
	}
	call := &Call{
		ServiceMethod: serviceMethod,
		Args:          args,
		Reply:         reply,
		Done:          done,
	}
	c.send(call)
	return call
}

func (c *Client) Call(serviceMethod string, args, reply interface{}) error {
	call := <-c.Go(serviceMethod, args, reply, make(chan *Call, 1)).Done
	return call.Error
}
