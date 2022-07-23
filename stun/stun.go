package stun

import (
    "fmt"
    "io"
    "net"
    "time"

    "github.com/pion/stun"
)

type message struct {
    data []byte
    addr *net.UDPAddr
}

type StunSocket struct {
    Conn       *net.UDPConn
    stun       *stun.Client
    publicAddr net.UDPAddr
    messages   chan message
}

func demultiplex(conn *net.UDPConn, stunConn io.Writer, messages chan message) {
    buf := make([]byte, 65536)
    for {
        n, raddr, err := conn.ReadFrom(buf)
        if err != nil {
            if err != io.EOF {
                panic(err)
            }
            return
        }

        if stun.IsMessage(buf[:n]) {
            stunConn.Write(buf[:n])
        } else {
            d := make([]byte, n)
            copy(d, buf[:n])
            messages <- message {
                data: d,
                addr: raddr.(*net.UDPAddr),
            }
        }
    }
}

func multiplex(conn *net.UDPConn, stunConn io.Reader, stunAddr net.Addr) {
    buf := make([]byte, 1024)
    for {
        n, err := stunConn.Read(buf)
        if err != nil {
            if err != io.EOF {
                panic(err)
            }
            return
        }
        conn.WriteTo(buf[:n], stunAddr)
    }
}

func keepAlive(c *stun.Client) {
    t := time.NewTicker(5 * time.Second)
    defer t.Stop()

    for range t.C {
        var stunErr error
        err := c.Do(stun.MustBuild(stun.TransactionID, stun.BindingRequest), func(res stun.Event) {
            stunErr = res.Error
        })

        if stunErr != nil || err != nil {
            return
        }
    }
}

func New(stunServer string) (*StunSocket, error) {
    //if your network does NAT on ipv6 you have serious problems
    stunServerAddr, err := net.ResolveUDPAddr("udp4", stunServer)
    if err != nil {
        return nil, fmt.Errorf("Unable to resolve IPv4 address of STUN server: %w", err)
    }

    stunL, stunR := net.Pipe()

    c, err := stun.NewClient(stunR)
    if err != nil {
        return nil, fmt.Errorf("Unable to create STUN client: %w", err)
    }

    addr, err := net.ResolveUDPAddr("udp4", "0.0.0.0:0")
    if err != nil {
        panic(err)
    }

    conn, err := net.ListenUDP("udp4", addr)
    if err != nil {
        return nil, fmt.Errorf("Unable to create UDP socket: %w", err)
    }

    messages := make(chan message)

    go demultiplex(conn, stunL, messages)
    go multiplex(conn, stunL, stunServerAddr)

    var stunAddr stun.XORMappedAddress
    var stunErr  error
    if err = c.Do(stun.MustBuild(stun.TransactionID, stun.BindingRequest), func(res stun.Event) {
        if res.Error != nil {
            stunErr = res.Error
            return
        }

        var xorAddr stun.XORMappedAddress
        if err := xorAddr.GetFrom(res.Message); err != nil {
            stunErr = err
            return
        }

        stunAddr.IP = append(stunAddr.IP, xorAddr.IP...)
        stunAddr.Port = xorAddr.Port
    }); err != nil {
        conn.Close()
        c.Close()
        return nil, fmt.Errorf("Failed to obtain public IP via STUN: %w", err)
    }
    if stunErr != nil {
        conn.Close()
        c.Close()
        return nil, fmt.Errorf("Failed to obtain public IP via STUN: %w", stunErr)
    }

    go keepAlive(c)

    return &StunSocket {
        Conn:       conn,
        stun:       c,
        publicAddr: net.UDPAddr {
            IP:   stunAddr.IP,
            Port: stunAddr.Port,
        },
        messages:   messages,
    }, nil
}

func (s *StunSocket) Close() error {
    s.stun.Close()
    return s.Conn.Close()
}

func (s *StunSocket) PublicAddr() *net.UDPAddr {
    return &s.publicAddr
}

func (s *StunSocket) Read() ([]byte, *net.UDPAddr, error) {
    m, ok := <-s.messages
    if !ok {
        return nil, nil, io.EOF
    }
    return m.data, m.addr, nil
}

func (s *StunSocket) WriteTo(data []byte, to net.Addr) (int, error) {
    return s.Conn.WriteTo(data, to)
}

