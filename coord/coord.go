package coord

import (
    "context"
    "encoding/json"
    "flag"
    "fmt"
    "log"
    "net"
    "net/http"
    "net/netip"
    "strconv"
    "sync"
    "time"

    "github.com/gorilla/websocket"
    "github.com/peterbourgon/ff/v3/ffcli"
)

type PeerList struct {
    Peers []Peer `json:"peers"`
}

type Peer struct {
    Name     string
    IP       net.IP    `json:"ip"`
    Port     uint16    `json:"port"`
    LastSeen time.Time `json:"last_seen"`
}

func (p *Peer) IPPort() netip.AddrPort {
    ip, _ := netip.AddrFromSlice(p.IP)
    return netip.AddrPortFrom(ip, uint16(p.Port))
}

type jsonPeer struct {
    Name     string `json:"name"`
    IP       string `json:"ip"`
    Port     uint16 `json:"port"`
    LastSeen int64  `json:"last_seen"`
}

func (p Peer) MarshalJSON() ([]byte, error) {
    return json.Marshal(jsonPeer {
        Name:     p.Name,
        IP:       p.IP.String(),
        Port:     p.Port,
        LastSeen: p.LastSeen.UnixMilli(),
    })
}

func (p *Peer) UnmarshalJSON(data []byte) error {
    var j jsonPeer
    if err := json.Unmarshal(data, &j); err != nil {
        return err
    }

    ip := net.ParseIP(j.IP)
    if ip == nil {
        return fmt.Errorf("Malformed IP address '%s'", j.IP)
    }
    if ip4 := ip.To4(); ip4 != nil {
        ip = ip4
    }

    p.Name = j.Name
    p.IP = ip
    p.Port = j.Port
    p.LastSeen = time.UnixMilli(j.LastSeen)

    return nil
}

type topic struct {
    mu            sync.Mutex
    peers         map[string]*Peer
    notifications map[string]chan struct{}
    peerList      []Peer
}

func (t *topic) peerMap() map[string]*Peer {
    if t.peers == nil {
        t.peers = make(map[string]*Peer)
    }
    return t.peers
}

func (t *topic) notificationMap() map[string]chan struct{} {
    if t.notifications == nil {
        t.notifications = make(map[string]chan struct{})
    }
    return t.notifications
}

func (t *topic) tryRegister(name string, ip net.IP, port uint16) (bool, chan struct{}) {
    t.mu.Lock()
    defer t.mu.Unlock()

    peers := t.peerMap()
    if _, ok := peers[name]; ok {
        return false, nil
    }

    peers[name] = &Peer {
        Name:     name,
        IP:       ip,
        Port:     port,
        LastSeen: time.Now(),
    }

    ch := make(chan struct{}, 1)
    t.notificationMap()[name] = ch

    t.peersChanged()

    return true, ch
}

func (t *topic) updateLastSeen(name string) {
    t.mu.Lock()
    defer t.mu.Unlock()
    if v, ok := t.peerMap()[name]; ok {
        v.LastSeen = time.Now()
        t.peerList = nil
    }
}

func (t *topic) unregister(name string) {
    t.mu.Lock()
    defer t.mu.Unlock()

    close(t.notificationMap()[name])
    delete(t.notificationMap(), name)
    delete(t.peerMap(), name)
    t.peersChanged()
}

func (t *topic) peersChanged() {
    t.peerList = nil

    for _, ch := range t.notificationMap() {
        select {
            case ch <- struct{}{}:
            default:
        }
    }
}

func (t *topic) getPeerList() []Peer {
    t.mu.Lock()
    defer t.mu.Unlock()

    if t.peerList != nil {
        return t.peerList
    }

    s := []Peer(nil)
    for _, peer := range t.peerMap() {
        s = append(s, *peer)
    }
    t.peerList = s
    return t.peerList
}

type state struct {
    mu     sync.Mutex
    topics map[string]*topic
}

func (s *state) topic(name string) *topic {
    s.mu.Lock()
    defer s.mu.Unlock()

    if s.topics == nil {
        s.topics = make(map[string]*topic)
    }

    if t, ok := s.topics[name]; ok {
        return t
    }

    t := &topic {}
    s.topics[name] = t
    return t
}

var port int

var fs = (func() *flag.FlagSet {
    fs := flag.NewFlagSet("coord", flag.ExitOnError)
    fs.IntVar(&port, "port", 6969, "Port to listen on")
    return fs
})()



var Command = &ffcli.Command {
    Name:       "coord",
    ShortUsage: "coord [flags]",
    ShortHelp:  "Coordination server for peer discovery",
    FlagSet:    fs,
    Exec:       func(ctx context.Context, args []string) error {
        var s state
        upgrader := websocket.Upgrader{}

        http.HandleFunc("/websocket", func(w http.ResponseWriter, r *http.Request) { 
            q := r.URL.Query()

            topic := q.Get("topic")
            if topic == "" {
                http.Error(w, "Missing topic", 400)
                return
            }

            name := q.Get("name")
            if name == "" {
                http.Error(w, "Missing name", 400)
            }

            ipRaw := q.Get("ip")
            ip, err := netip.ParseAddr(ipRaw)
            if err != nil {
                http.Error(w, "Missing or invalid ip", 400)
                return
            }

            portRaw := q.Get("port")
            port, err := strconv.ParseUint(portRaw, 10, 16)
            if err != nil || port == 0 {
                http.Error(w, "Missing or invalid port", 400)
                return
            }

            t := s.topic(topic)
            ok, ch := t.tryRegister(name, net.IP(ip.AsSlice()), uint16(port))
            if !ok {
                http.Error(w, "Client with that name already exists", 401)
                return
            }
            defer t.unregister(name)

            ws, err := upgrader.Upgrade(w, r, nil)
            if err != nil {
                return
            }
            defer ws.Close()

            ws.SetPingHandler(func (_ string) error {
                t.updateLastSeen(name)
                return nil
            })

            go func() {
                for {
                    _, more := <-ch
                    if !more {
                        break
                    }
                    peers := t.getPeerList()
                    if err := ws.WriteJSON(PeerList {
                        Peers: peers,
                    }); err != nil {
                        ws.Close()
                        break
                    }
                }
            }()
            for {
                if _, _, err := ws.NextReader(); err != nil {
                    break
                }
            }
        })

        log.Printf("Listening on :%d", port)
        return http.ListenAndServe(fmt.Sprintf(":%d", port), nil)
    },
}

