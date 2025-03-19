package main

import (
	"go/ast"
	"log"
	"reflect"
	"sync/atomic"
)

type methodType struct {
	method    reflect.Method
	ArgType   reflect.Type
	ReplyType reflect.Type
	numCalls  uint64
}

func (m *methodType) NumCalls() uint64 {
	return atomic.LoadUint64(&m.numCalls)
}

func (m *methodType) newArgs() reflect.Value {
	var args reflect.Value
	if m.ArgType.Kind() == reflect.Ptr {
		args = reflect.New(m.ArgType.Elem())
	} else {
		args = reflect.New(m.ArgType).Elem()
	}
	return args
}

func (m *methodType) newReply() reflect.Value {
	reply := reflect.New(m.ReplyType.Elem())
	switch m.ReplyType.Elem().Kind() {
	case reflect.Map:
		reply.Elem().Set(reflect.MakeMap(m.ReplyType.Elem()))
	case reflect.Slice:
		reply.Elem().Set(reflect.MakeSlice(m.ReplyType.Elem(), 0, 0))
	}
	return reply
}

type service struct {
	name   string
	typ    reflect.Type
	rcv    reflect.Value
	method map[string]*methodType
}

func newService(rcv interface{}) *service {
	s := new(service)
	s.rcv = reflect.ValueOf(rcv)
	s.name = reflect.Indirect(s.rcv).Type().Name()
	s.typ = reflect.TypeOf(rcv)
	if !ast.IsExported(s.name) {
		log.Fatalf("rpc service: %s is not a exporting service name", s.name)
	}
	s.registerMethods()
	return s
}

func (s *service) registerMethods() {
	s.method = make(map[string]*methodType)
	for i := 0; i < s.typ.NumMethod(); i++ {
		method := s.typ.Method(i)
		mTyp := method.Type
		// 检查入参数量是否为3个 返回数量是否为一个
		if mTyp.NumIn() != 3 || mTyp.NumOut() != 1 {
			continue
		}
		// 检查返回类型是否为error类型
		if mTyp.Out(0) != reflect.TypeOf((*error)(nil)).Elem() {
			continue
		}
		argType := mTyp.In(1)
		replyType := mTyp.In(2)
		if !isExportedOrBuiltinType(argType) || !isExportedOrBuiltinType(replyType) {
			continue
		}

		s.method[method.Name] = &methodType{
			method:    method,
			ArgType:   argType,
			ReplyType: replyType,
		}
		log.Printf("rpc service: %s registered method: %s", s.name, method.Name)
	}
}

func (s *service) call(m *methodType, argv, replyv reflect.Value) error {
	atomic.AddUint64(&m.numCalls, 1)
	f := m.method.Func
	returnValues := f.Call([]reflect.Value{s.rcv, argv, replyv})
	if errInter := returnValues[0].Interface(); errInter != nil {
		return errInter.(error)
	}
	return nil
}

func isExportedOrBuiltinType(t reflect.Type) bool {
	// 检查是否为到处类型或内置类型（内置类型没有包路径）
	return ast.IsExported(t.Name()) || t.PkgPath() == ""
}
