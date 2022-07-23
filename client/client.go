package client

import (
    "bufio"
    "context"
    "encoding/binary"
    "flag"
    "fmt"
    "log"
    "math/rand"
    "net"
    "os"

    "github.com/natanbc/ssc0904-nat-traversal/stun"

    "github.com/peterbourgon/ff/v3/ffcli"
)

var (
    coordinationServer string
    stunServer         string
)

var fs = (func() *flag.FlagSet {
    fs := flag.NewFlagSet("client", flag.ExitOnError)
    fs.StringVar(&coordinationServer, "coordination-server", "https://ssc0904-coord.natanbc.net", "Coordination server to use")
    fs.StringVar(&stunServer,         "stun-server",         "stun.l.google.com:19302",           "STUN server to use")
    return fs
})()

const magicData uint64 = 0x4441544144415441 //DATADATA
const magicPing uint64 = 0x50494e4750494e47 //PINGPING

func makeDataMessage(data []byte) []byte {
    b := make([]byte, len(data) + 128)
    //for some godforsaken reason my NAT drops small UDP packets
    _, _  = rand.Read(b[:128])
    copy(b[128:], data)
    binary.BigEndian.PutUint64(b[:8], magicData)
    return b
}

func makePingMessage() []byte {
    b := make([]byte, 128)
    //for some godforsaken reason my NAT drops small UDP packets
    _, _ = rand.Read(b)
    binary.BigEndian.PutUint64(b[:8], magicPing)
    return b
}

func parseMessage(msg []byte) ([]byte, uint64, error) {
    if len(msg) < 8 {
        return nil, 0, fmt.Errorf("Message too small")
    }
    magic := binary.BigEndian.Uint64(msg[:8])
    if magic == magicData {
        return msg[128:], magic, nil
    }
    if magic == magicPing {
        return nil, magic, nil
    }
    return nil, 0, fmt.Errorf("Unknown magic %X", magic)
}

var Command = &ffcli.Command {
    Name:       "client",
    ShortUsage: "client [flags] <topic> <name>",
    ShortHelp:  "Starts a client and connects to other clients on the same topic",
    FlagSet:    fs,
    Exec:       func(ctx context.Context, args []string) error {
        if len(args) != 2 {
            return flag.ErrHelp
        }
        topic := args[0]
        name  := args[1]

        s, err := stun.New(stunServer)
        if err != nil {
            return err
        }
        log.Printf("Local address:  %s", s.Conn.LocalAddr().String())
        log.Printf("Public address: %s", s.PublicAddr().String())

        ping := func(addr *net.UDPAddr) {
            s.Conn.WriteTo(makePingMessage(), addr)
        }

        peers, err := newPeerRegistry(coordinationServer, topic, name, s.PublicAddr(), ping)
        if err != nil {
            return err
        }

        go func() {
            reader := bufio.NewReader(os.Stdin)
            for {
                text, err := reader.ReadBytes('\n')
                if err != nil {
                    log.Printf("Input closed")
                    return
                }
                if len(text) > 0 && text[len(text) - 1] == '\n' {
                    text = text[:len(text) - 1]
                }
                if len(text) > 0 && text[len(text) - 1] == '\r' {
                    text = text[:len(text) - 1]
                }
                if len(text) == 0 {
                    continue
                }
                log.Printf("Sending message '%s'", string(text))
                msg := makeDataMessage(text)
                peers.forEachPeerAddress(func(addr *net.UDPAddr) {
                    log.Printf("Sending data packet to %v", addr)
                    s.Conn.WriteTo(msg, addr)
                })
            }
        }()

        for {
            msg, sender, err := s.Read()
            if err != nil {
                return err
            }
            peerName := peers.peerName(sender)

            data, typ, err := parseMessage(msg)
            if err != nil {
                log.Printf("[%s aka %s]: %v", sender.String(), peerName, err)
                continue
            }
            if typ == magicPing {
                peers.onPing(sender)
                continue
            }
            log.Printf("[%s aka %s]: %s", sender.String(), peerName, string(data))
        }
    },
}

