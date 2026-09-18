// Package clientkey 提供轻量客户端 API Key 管理与用量累加，
// 供个人给不同客户端（如 Cursor、Cherry Studio、手机）分发独立 Key 并打标签。
package clientkey

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// KeyInfo 单个客户端 Key 记录。
type KeyInfo struct {
	Key         string    `json:"key"`          // 形如 sk-zg-xxxxxxxx
	Name        string    `json:"name"`         // 客户端名称/备注（如 Cursor-台式机）
	CreatedAt   time.Time `json:"created_at"`   // 创建时间
	LastUsedAt  time.Time `json:"last_used_at"`  // 最近一次调用时间
	TotalTokens int64     `json:"total_tokens"` // 累计消耗 Token
	Enabled     bool      `json:"enabled"`      // 是否启用
}

// Store 客户端 Key 存储与鉴权器。
type Store struct {
	mu   sync.RWMutex
	fp   string
	keys map[string]*KeyInfo
}

// New 创建或加载客户端 Key 存储。
func New(fp string) *Store {
	s := &Store{
		fp:   fp,
		keys: make(map[string]*KeyInfo),
	}
	s.load()
	return s
}

func (s *Store) load() {
	if s.fp == "" {
		return
	}
	raw, err := os.ReadFile(s.fp)
	if err != nil {
		return
	}
	var list []*KeyInfo
	if err := json.Unmarshal(raw, &list); err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range list {
		if k.Key != "" {
			s.keys[k.Key] = k
		}
	}
}

func (s *Store) saveLocked() {
	if s.fp == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(s.fp), 0o755)
	list := make([]*KeyInfo, 0, len(s.keys))
	for _, k := range s.keys {
		list = append(list, k)
	}
	raw, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return
	}
	tmp := s.fp + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err == nil {
		_ = os.Rename(tmp, s.fp)
	}
}

// List 返回所有客户端 Key 列表（按创建时间降序）。
func (s *Store) List() []KeyInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]KeyInfo, 0, len(s.keys))
	for _, k := range s.keys {
		out = append(out, *k)
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[i].CreatedAt.Before(out[j].CreatedAt) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// Create 生成并保存一个新的客户端 Key。
func (s *Store) Create(name string) (*KeyInfo, error) {
	if name == "" {
		name = "未命名客户端"
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}
	keyStr := "sk-zg-" + hex.EncodeToString(b)

	info := &KeyInfo{
		Key:       keyStr,
		Name:      name,
		CreatedAt: time.Now(),
		Enabled:   true,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys[keyStr] = info
	s.saveLocked()
	return info, nil
}

// Delete 删除指定 Key。
func (s *Store) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.keys[key]; !ok {
		return fmt.Errorf("key not found")
	}
	delete(s.keys, key)
	s.saveLocked()
	return nil
}

// Toggle 启用或禁用指定 Key。
func (s *Store) Toggle(key string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.keys[key]
	if !ok {
		return fmt.Errorf("key not found")
	}
	k.Enabled = enabled
	s.saveLocked()
	return nil
}

// Validate 校验 Key 是否合法且启用，返回对应客户端名称。
func (s *Store) Validate(key string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	k, ok := s.keys[key]
	if !ok || !k.Enabled {
		return "", false
	}
	return k.Name, true
}

// RecordUsage 累加 Token 消耗与刷新最后使用时间。
func (s *Store) RecordUsage(key string, tokens int64) {
	if key == "" || tokens <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if k, ok := s.keys[key]; ok {
		k.TotalTokens += tokens
		k.LastUsedAt = time.Now()
		s.saveLocked()
	}
}
