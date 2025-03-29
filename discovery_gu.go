package main

import (
	"log"
	"net/http"
	"strings"
	"time"
)

type GuRegistryDiscovery struct {
	*MultiServersDiscovery
	registry   string        // 注册中心地址
	timeout    time.Duration // 服务列表过期时间
	lastUpdate time.Time     // 最后从注册中心更新列表的时间
}

const defaultUpdateTimeout = time.Second * 10

// NewGuRegistryDiscovery 新建注册发现中心
func NewGuRegistryDiscovery(registerAddr string, timeout time.Duration) *GuRegistryDiscovery {
	if timeout == 0 {
		timeout = defaultUpdateTimeout
	}

	d := &GuRegistryDiscovery{
		MultiServersDiscovery: NewMultiServerDiscovery(make([]string, 0)),
		registry:              registerAddr,
		timeout:               timeout,
	}
	return d
}

func (d *GuRegistryDiscovery) Update(servers []string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.servers = servers
	d.lastUpdate = time.Now()
	return nil
}

func (d *GuRegistryDiscovery) Refresh() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	// 未过期
	if d.lastUpdate.Add(d.timeout).After(time.Now()) {
		return nil
	}
	log.Println("refresh registry:", d.registry)

	// 超时刷新
	resp, err := http.Get(d.registry)
	if err != nil {
		log.Println("refresh gu registry error:", err)
		return err
	}

	servers := strings.Split(resp.Header.Get("X-GuRPC-Servers"), ",")
	d.servers = make([]string, 0, len(servers))
	for _, server := range servers {
		if strings.TrimSpace(server) != "" {
			d.servers = append(d.servers, strings.TrimSpace(server))
		}
	}
	d.lastUpdate = time.Now()
	return nil
}

func (d *GuRegistryDiscovery) Get(mode SelectMode) (string, error) {
	if err := d.Refresh(); err != nil {
		return "", err
	}
	return d.MultiServersDiscovery.Get(mode)
}

func (d *GuRegistryDiscovery) GetAll() ([]string, error) {
	if err := d.Refresh(); err != nil {
		return nil, err
	}
	return d.MultiServersDiscovery.GetAll()
}
