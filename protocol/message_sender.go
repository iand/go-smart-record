package protocol

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p-core/host"
	"github.com/libp2p/go-libp2p-core/network"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/libp2p/go-libp2p-core/protocol"
	"github.com/libp2p/go-msgio"

	pb "github.com/libp2p/go-smart-record/protocol/pb"
)

// ErrReadTimeout is an error that occurs when no message is read within the timeout period.
var ErrReadTimeout = fmt.Errorf("timed out reading response")

// messageSenderImpl is responsible for sending requests and messages to peers efficiently, including reuse of streams.
// It also tracks metrics for sent requests and messages.
type messageSenderImpl struct {
	host      host.Host // the network services we need
	smlk      sync.Mutex
	strmap    map[peer.ID]*peerMessageSender
	protocols []protocol.ID
}

func (m *messageSenderImpl) streamDisconnect(ctx context.Context, p peer.ID) {
	m.smlk.Lock()
	defer m.smlk.Unlock()
	ms, ok := m.strmap[p]
	if !ok {
		return
	}
	delete(m.strmap, p)

	// Do this asynchronously as ms.lk can block for a while.
	go func() {
		if err := ms.lk.Lock(ctx); err != nil {
			return
		}
		defer ms.lk.Unlock()
		ms.invalidate()
	}()
}

// SendRequest sends out a request, but also makes sure to
// measure the RTT for latency measurements.
func (m *messageSenderImpl) SendRequest(ctx context.Context, p peer.ID, pmes *pb.Message) (*pb.Message, error) {

	ms, err := m.messageSenderForPeer(ctx, p)
	if err != nil {
		log.Debugw("request failed to open message sender", "error", err, "to", p)
		return nil, err
	}

	rpmes, err := ms.SendRequest(ctx, pmes)
	if err != nil {
		return nil, err
	}

	return rpmes, nil
}

// SendMessage sends out a message
func (m *messageSenderImpl) SendMessage(ctx context.Context, p peer.ID, pmes *pb.Message) error {

	ms, err := m.messageSenderForPeer(ctx, p)
	if err != nil {
		log.Debugw("message failed to open message sender", "error", err, "to", p)
		return err
	}

	if err := ms.SendMessage(ctx, pmes); err != nil {
		log.Debugw("message failed", "error", err, "to", p)
		return err
	}

	return nil
}

func (m *messageSenderImpl) messageSenderForPeer(ctx context.Context, p peer.ID) (*peerMessageSender, error) {
	m.smlk.Lock()
	ms, ok := m.strmap[p]
	if ok {
		m.smlk.Unlock()
		return ms, nil
	}
	ms = &peerMessageSender{p: p, m: m, lk: newCtxMutex()}
	m.strmap[p] = ms
	m.smlk.Unlock()

	if err := ms.prepOrInvalidate(ctx); err != nil {
		m.smlk.Lock()
		defer m.smlk.Unlock()

		if msCur, ok := m.strmap[p]; ok {
			// Changed. Use the new one, old one is invalid and
			// not in the map so we can just throw it away.
			if ms != msCur {
				return msCur, nil
			}
			// Not changed, remove the now invalid stream from the
			// map.
			delete(m.strmap, p)
		}
		// Invalid but not in map. Must have been removed by a disconnect.
		return nil, err
	}
	// All ready to go.
	return ms, nil
}

// peerMessageSender is responsible for sending requests and messages to a particular peer
type peerMessageSender struct {
	s  network.Stream
	r  msgio.ReadCloser
	lk ctxMutex
	p  peer.ID
	m  *messageSenderImpl

	invalid   bool
	singleMes int
}

// invalidate is called before this peerMessageSender is removed from the strmap.
// It prevents the peerMessageSender from being reused/reinitialized and then
// forgotten (leaving the stream open).
func (ms *peerMessageSender) invalidate() {
	ms.invalid = true
	if ms.s != nil {
		_ = ms.s.Reset()
		ms.s = nil
	}
}

func (ms *peerMessageSender) prepOrInvalidate(ctx context.Context) error {
	if err := ms.lk.Lock(ctx); err != nil {
		return err
	}
	defer ms.lk.Unlock()

	if err := ms.prep(ctx); err != nil {
		ms.invalidate()
		return err
	}
	return nil
}

func (ms *peerMessageSender) prep(ctx context.Context) error {
	if ms.invalid {
		return fmt.Errorf("message sender has been invalidated")
	}
	if ms.s != nil {
		return nil
	}

	// We only want to speak to peers using our primary protocols. We do not want to query any peer that only speaks
	// one of the secondary "server" protocols that we happen to support (e.g. older nodes that we can respond to for
	// backwards compatibility reasons).
	nstr, err := ms.m.host.NewStream(ctx, ms.p, ms.m.protocols...)
	if err != nil {
		return err
	}

	ms.r = msgio.NewVarintReaderSize(nstr, network.MessageSizeMax)
	ms.s = nstr

	return nil
}

func (ms *peerMessageSender) SendMessage(ctx context.Context, pmes *pb.Message) error {
	if err := ms.lk.Lock(ctx); err != nil {
		return err
	}
	defer ms.lk.Unlock()

	if err := ms.prep(ctx); err != nil {
		return err
	}

	if err := ms.writeMsg(pmes); err != nil {
		_ = ms.s.Reset()
		ms.s = nil

		log.Debugw("error writing message", "error", err, "retrying", true)
		return err
	}

	return nil
}

func (ms *peerMessageSender) SendRequest(ctx context.Context, pmes *pb.Message) (*pb.Message, error) {
	if err := ms.lk.Lock(ctx); err != nil {
		return nil, err
	}
	defer ms.lk.Unlock()

	if err := ms.prep(ctx); err != nil {
		return nil, err
	}

	if err := ms.writeMsg(pmes); err != nil {
		_ = ms.s.Reset()
		ms.s = nil
		log.Debugw("error writing message", "error", err)
		return nil, err
	}

	mes := new(pb.Message)
	if err := ms.ctxReadMsg(ctx, mes); err != nil {
		_ = ms.s.Reset()
		ms.s = nil

		log.Debugw("error reading message", "error", err)
		return nil, err
	}

	return mes, nil
}

func (ms *peerMessageSender) writeMsg(pmes *pb.Message) error {
	return writeMsg(ms.s, pmes)
}

func (ms *peerMessageSender) ctxReadMsg(ctx context.Context, mes *pb.Message) error {
	errc := make(chan error, 1)
	go func(r msgio.ReadCloser) {
		defer close(errc)
		bytes, err := r.ReadMsg()
		defer r.ReleaseMsg(bytes)
		if err != nil {
			errc <- err
			return
		}
		errc <- mes.Unmarshal(bytes)
	}(ms.r)

	t := time.NewTimer(readMessageTimeout)
	defer t.Stop()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return ErrReadTimeout
	}
}
