package rules

import (
	"strings"
	"time"

	"github.com/coreos/etcd/client"
	"github.com/coreos/etcd/clientv3"
	"golang.org/x/net/context"
)

type baseReadAPI struct {
	cancelFunc context.CancelFunc
}

func (bra *baseReadAPI) getContext() context.Context {
	var ctx context.Context
	ctx, bra.cancelFunc = context.WithTimeout(context.Background(), time.Duration(60)*time.Second)
	return ctx
}

func (bra *baseReadAPI) cancel() {
	bra.cancelFunc()
}

type etcdReadAPI struct {
	baseReadAPI
	keysAPI client.KeysAPI
}

func (edra *etcdReadAPI) get(key string) (*string, error) {
	ctx := edra.getContext()
	defer edra.cancel()
	resp, err := edra.keysAPI.Get(ctx, key, nil)
	if err != nil {
		if !strings.HasPrefix(err.Error(), "100") {
			return nil, err
		}
		return nil, nil
	}
	return &resp.Node.Value, nil
}

type etcdV3ReadAPI struct {
	baseReadAPI
	kV clientv3.KV
}

func (edv3ra *etcdV3ReadAPI) get(key string) (*string, error) {
	ctx := edv3ra.baseReadAPI.getContext()
	defer edv3ra.cancel()
	resp, err := edv3ra.kV.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	if resp.Count == 0 {
		return nil, nil
	}
	val := string(resp.Kvs[0].Value[:])
	return &val, nil
}

type keyWatcher interface {
	next() (string, *string, error)
}

func newEtcdKeyWatcher(api client.KeysAPI, prefix string, timeout time.Duration) keyWatcher {
	w := api.Watcher(prefix, &client.WatcherOptions{
		Recursive: true,
	})
	watcher := etcdKeyWatcher{
		baseKeyWatcher: baseKeyWatcher{
			timeout: timeout,
		},
		w: w,
	}
	return &watcher
}

func newEtcdV3KeyWatcher(watcher clientv3.Watcher, prefix string, timeout time.Duration) keyWatcher {
	kw := etcdV3KeyWatcher{
		baseKeyWatcher: baseKeyWatcher{
			timeout: timeout,
		},
		prefix: prefix,
		w:      watcher,
	}
	return &kw
}

type baseKeyWatcher struct {
	cancelFunc context.CancelFunc
	timeout    time.Duration
}

func (bkw *baseKeyWatcher) getContext() context.Context {
	ctx := context.Background()
	if bkw.timeout > 0 {
		ctx, bkw.cancelFunc = context.WithTimeout(ctx, bkw.timeout)
	}
	return ctx
}

type etcdKeyWatcher struct {
	baseKeyWatcher
	w client.Watcher
}

func (ekw *etcdKeyWatcher) next() (string, *string, error) {
	defer ekw.cancel()
	resp, err := ekw.w.Next(ekw.getContext())
	if err != nil {
		return "", nil, err
	}
	ekw.cancelFunc = nil
	node := resp.Node
	if resp.Action == "delete" || resp.Action == "expire" {
		return node.Key, nil, nil
	}
	return node.Key, &node.Value, nil
}

func (bkw *baseKeyWatcher) cancel() {
	if bkw.cancelFunc != nil {
		bkw.cancelFunc()
		bkw.cancelFunc = nil
	}
}

type etcdV3KeyWatcher struct {
	baseKeyWatcher
	ch         clientv3.WatchChan
	eventIndex int
	events     []*clientv3.Event
	prefix     string
	w          clientv3.Watcher
}

func (ev3kw *etcdV3KeyWatcher) next() (string, *string, error) {
	defer ev3kw.cancel()
	if ev3kw.ch == nil {
		ev3kw.ch = ev3kw.w.Watch(ev3kw.getContext(), ev3kw.prefix, clientv3.WithPrefix())
	}
	if ev3kw.events == nil {
		ev3kw.eventIndex = 0
		wr := <-ev3kw.ch
		ev3kw.events = wr.Events
	}
	if len(ev3kw.events) == 0 {
		ev3kw.events = nil
		// This avoids a potential endless loop due to a closed channel
		ev3kw.ch = nil
		return ev3kw.next()
	}
	event := ev3kw.events[ev3kw.eventIndex]
	ev3kw.eventIndex = ev3kw.eventIndex + 1
	if ev3kw.eventIndex >= len(ev3kw.events) {
		ev3kw.events = nil
	}
	key := string(event.Kv.Key[:])
	if event.Type == clientv3.EventTypeDelete { // Expire?
		return key, nil, nil
	}
	val := string(event.Kv.Value[:])
	return key, &val, nil
}
