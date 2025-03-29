package registry

import (
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

type GuRegistry struct {
	timeout time.Duration
	mu      sync.Mutex
	servers map[string]*ServerItem
}

type ServerItem struct {
	Addr  string
	start time.Time
}

const (
	defaultPath    = "/_gurpc_/registry"
	defaultTimeout = time.Minute * 5
)

func New(timeout time.Duration) *GuRegistry {
	return &GuRegistry{
		servers: make(map[string]*ServerItem),
		timeout: timeout,
	}
}

var DefaultGuRegister = New(defaultTimeout)

func (r *GuRegistry) putServer(addr string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.servers[addr]
	if s == nil {
		r.servers[addr] = &ServerItem{addr, time.Now()}
	} else {
		s.start = time.Now()
	}
}

// 筛选存活服务
func (r *GuRegistry) aliveServers() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var alive []string
	for addr, s := range r.servers {
		// 如果超时时间为0或服务未超时
		if r.timeout == 0 || s.start.Add(r.timeout).After(time.Now()) {
			alive = append(alive, addr)
		} else {
			delete(r.servers, addr)
		}
	}
	sort.Strings(alive)
	return alive
}

func (r *GuRegistry) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case "GET":
		// 获取所有可用的服务列表
		w.Header().Set("X-GuRPC-Servers", strings.Join(r.aliveServers(), ","))
	case "POST":
		// 添加服务实例或发送心跳包
		addr := req.Header.Get("X-GuRPC-Servers")
		if addr == "" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		r.putServer(addr)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// HandleHTTP 注册HTTP处理器
func (r *GuRegistry) HandleHTTP(registryPath string) {
	http.Handle(registryPath, r)
	log.Println("registry:", registryPath)
}

func HandleHTTP() {
	DefaultGuRegister.HandleHTTP(defaultPath)
}

func Heartbeat(registry, addr string, duration time.Duration) {
	if duration == 0 {
		// 确保时间间隔小于过期时间
		duration = defaultTimeout - time.Duration(1)*time.Minute
	}
	var err error
	err = sendHeartbeat(registry, addr)
	// 启动协程专用于发送心跳包
	go func() {
		t := time.NewTicker(duration)
		for err == nil {
			<-t.C
			err = sendHeartbeat(registry, addr)
		}
	}()
}

func sendHeartbeat(registry, addr string) error {
	log.Println("rpc server heartbeat:", addr)
	httpClient := &http.Client{}
	req, _ := http.NewRequest("POST", registry, nil)
	req.Header.Set("X-GuRPC-Servers", addr)
	if _, err := httpClient.Do(req); err != nil {
		log.Println("rpc server heartbeat error:", err)
		return err
	}
	return nil
}
