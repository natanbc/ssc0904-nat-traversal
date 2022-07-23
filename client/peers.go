package client

import (
    "encoding/json"
    "fmt"
    "log"
    "net"
    "net/netip"
    "net/url"
    "path"
    "sync"
    "time"

    "github.com/natanbc/ssc0904-nat-traversal/coord"

    "github.com/gorilla/websocket"
)

type peerRegistry struct {
    mu          sync.Mutex
    baseUrl     *url.URL
    peers       map[netip.AddrPort]coord.Peer
    holepunched map[netip.AddrPort]struct{}
    selfPeer    coord.Peer
    topic       string
    doStop      bool
    socket      *websocket.Conn
}

func newPeerRegistry(baseUrl, topic, name string, selfAddr *net.UDPAddr, ping func(*net.UDPAddr)) (*peerRegistry, error) {
    base, err := url.Parse(baseUrl)
    if err != nil {
        return nil, fmt.Errorf("Unable to parse base url: %w", err)
    }

    p := &peerRegistry {
        baseUrl:     base,
        peers:       make(map[netip.AddrPort]coord.Peer),
        holepunched: make(map[netip.AddrPort]struct{}),
        selfPeer:    coord.Peer {
            Name: name,
            IP:   selfAddr.IP,
            Port: uint16(selfAddr.Port),
        },
        topic:       topic,
    }

    if err = p.connect(); err != nil {
        return nil, err
    }

    go func() {
        t := time.NewTicker(5 * time.Second)
        defer t.Stop()

        for range t.C {
            if p.shouldStop() {
                break
            }
            if err := p.socket.WriteMessage(websocket.PingMessage, []byte{}); err != nil {
                log.Printf("Failed to send ping message to server: %v", err)
                p.stop()
                break
            }
        }
    }()
    go func() {
        for {
            if p.shouldStop() {
                break
            }
            mt, message, err := p.socket.ReadMessage()
            if err != nil {
                log.Printf("Failed to read message from server: %v", err)
                p.stop()
                break
            }

            if mt == websocket.TextMessage {
                if err := p.handlePeerList(message); err != nil {
                    log.Printf("Failed to update peer list: %v", err)
                }
            }
        }
    }()
    go func() {
        t := time.NewTicker(250 * time.Millisecond)
        defer t.Stop()

        for range t.C {
            if p.shouldStop() {
                break
            }
            p.forEachPeerAddress(ping)
        }
    }()

    return p, nil
}

func (p *peerRegistry) shouldStop() bool {
    p.mu.Lock()
    defer p.mu.Unlock()
    return p.doStop
}

func (p *peerRegistry) stop() {
    p.mu.Lock()
    defer p.mu.Unlock()
    p.doStop = true
    p.socket.Close()
}

func (p *peerRegistry) connect() error {
    u := p.makeUrl("websocket", map[string]string {
        "topic": p.topic,
        "name":  p.selfPeer.Name,
        "ip":    p.selfPeer.IP.String(),
        "port":  fmt.Sprintf("%d", p.selfPeer.Port),
    })

    c, _, err := websocket.DefaultDialer.Dial(u, nil)
    if err != nil {
        return fmt.Errorf("Unable to establish websocket connection: %w", err)
    }

    p.socket = c
    return nil
}

func (p *peerRegistry) handlePeerList(raw []byte) error {
    var r coord.PeerList

    if err := json.Unmarshal(raw, &r); err != nil {
        return fmt.Errorf("Malformed response: %w", err)
    }

    p.mu.Lock()
    defer p.mu.Unlock()

    prev := p.peers
    discovered := make(map[netip.AddrPort]coord.Peer)
    next := make(map[netip.AddrPort]coord.Peer)

    for _, v := range r.Peers {
        k := v.IPPort()

        if _, ok := p.peers[k]; !ok {
            discovered[k] = v
        }

        delete(prev, k)
        next[k] = v
    }

    p.peers = next

    me := p.selfPeer.IPPort()

    for k, v := range prev {
        if k == me {
            continue
        }

        delete(p.holepunched, k)

        log.Printf("Peer %s (aka %s) disconnected", k.String(), v.Name)
    }
    for k, v := range discovered {
        if k == me {
            continue
        }

        log.Printf("New peer %s (aka %s)", k.String(), v.Name)
    }

    return nil
}

func (p *peerRegistry) onPing(addr *net.UDPAddr) {
    k := (&coord.Peer {
        IP:   addr.IP,
        Port: uint16(addr.Port),
    }).IPPort()

    p.mu.Lock()
    defer p.mu.Unlock()

    _, punched := p.holepunched[k]
    p.holepunched[k] = struct{}{}

    if !punched {
        p, ok := p.peers[k]
        if !ok {
            return
        }
        log.Printf("Established connection to peer %s (aka %s)", k.String(), p.Name)
    }
}

func (p *peerRegistry) peerName(addr *net.UDPAddr) string {
    k := (&coord.Peer {
        IP:   addr.IP,
        Port: uint16(addr.Port),
    }).IPPort()

    p.mu.Lock()
    defer p.mu.Unlock()
    if v, ok := p.peers[k]; ok {
        return v.Name
    }
    return "<unknown peer>"
}

func (p *peerRegistry) forEachPeerAddress(f func(*net.UDPAddr)) {
    me := p.selfPeer.IPPort()

    p.mu.Lock()
    defer p.mu.Unlock()

    for k, v := range p.peers {
        //ignore self
        if k == me {
            continue
        }

        f(&net.UDPAddr {
            IP:   v.IP,
            Port: int(v.Port),
        })
    }
}

func (p *peerRegistry) makeUrl(reqPath string, query map[string]string) string {
    url := *p.baseUrl
    if url.Scheme == "http" {
        url.Scheme = "ws"
    } else if url.Scheme == "https" {
        url.Scheme = "wss"
    }

    url.Path = path.Join(url.Path, reqPath)

    q := url.Query()
    for k, v := range query {
        q.Set(k, v)
    }
    url.RawQuery = q.Encode()

    return url.String()
}

