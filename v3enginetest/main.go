package main

import (
	"context"
	"fmt"
	"time"

	"github.com/IBM-Cloud/go-etcd-rules/rules"
	"github.com/coreos/etcd/clientv3"
	"github.com/uber-go/zap"
)

var (
	idCount   = 4
	pollCount = 5
)

const (
	dataPath  = "/data/:id"
	blockPath = "/block/:id"
)

type polled struct {
	ID        string
	pollCount int
}

func check(err error) {
	if err != nil {
		panic(err.Error())
	}
}

func main() {
	logger := zap.New(
		zap.NewJSONEncoder(zap.RFC3339Formatter("ts")),
		zap.AddCaller(),
		zap.DebugLevel,
	)
	cfg := clientv3.Config{Endpoints: []string{"http://127.0.0.1:2379"}}
	cl, err := clientv3.New(cfg)
	check(err)
	kv := clientv3.NewKV(cl)
	kv.Delete(context.Background(), "/", clientv3.WithPrefix())
	engine := rules.NewV3Engine(cfg, logger)
	preReq, err := rules.NewEqualsLiteralRule(dataPath, nil)
	check(err)
	preReq = rules.NewNotRule(preReq)
	block, err := rules.NewEqualsLiteralRule(blockPath, nil)
	check(err)
	preReq = rules.NewAndRule(preReq, block)
	ps := map[string]*polled{}
	done := make(chan *polled)
	for i := 0; i < idCount; i++ {
		id := fmt.Sprint(i)
		kv.Put(context.Background(), "/data/"+id, "0")
		p := polled{ID: id}
		ps[id] = &p
	}
	engine.AddPolling("/polling/:id", preReq, 2, func(task *rules.V3RuleTask) {
		p := ps[*task.Attr.GetAttribute("id")]
		path := task.Attr.Format(dataPath)
		task.Logger.Info("polling", zap.String("id", p.ID), zap.String("path", path))
		resp, err := kv.Get(task.Context, path) //keysAPI.Get(task.Context, path, nil)
		check(err)
		value := string(resp.Kvs[0].Value)
		task.Logger.Info("Compare pollcount", zap.String("id", p.ID), zap.String("etcd", value), zap.Int("local", p.pollCount))
		if value != fmt.Sprint(p.pollCount) {
			panic("Poll count does not match!")
		}
		if p.pollCount == pollCount {
			_, err = kv.Put(task.Context, task.Attr.Format(blockPath), "done")
			check(err)
			done <- p
			return
		}
		if p.pollCount > pollCount {
			panic("Poll count higher than max!")
		}
		p.pollCount++
		_, err = kv.Put(task.Context, path, fmt.Sprint(p.pollCount))
		check(err)
	})
	engine.Run()
	for i := 0; i < idCount; i++ {
		p := <-done
		logger.Info("Done", zap.String("ID", p.ID))
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(30)*time.Second)
	defer cancel()
	engine.Shutdown(ctx)
}
