# NAT Traversal

Example project implementing NAT traversal, with a centralized server for peer discovery.

## How it works

1) Each client discovers it's own public IP:port via [STUN](https://datatracker.ietf.org/doc/html/rfc8489)
2) Clients connect to the [coordination server](#coordination-server) to register themselves and discover peers
3) Clients send ping packets to all peers, so that packets sent by those peers are treated as replies and allowed through the firewall
4) Once they get data to send, they broadcast to all peers
5) Repeat steps 2-4

## Wire format

Packets sent to other peers start with an 8-byte magic value, followed 120 bytes of random data then the
packet's data. The magic value may be either 0x4441544144415441 (DATADATA) or 0x50494e4750494e47 (PINGPING),
in network byte order. Data packets contain application-level data after the header, PING packets don't
contain any data and should be ignored, existing only for firewall hole-punching.

The same socket is also used to send data to the STUN server, whose replies have 0x2112A442 in network byte
order on bytes 4:8, which is why the magic values above cover this byte range, so data/ping packets don't get
mistaken for STUN packets.

The 120 bytes of random data exist because some NATs (like mine) decide to drop small packets, but not large
packets. The size is purely arbitrary, I just picked one that made the header size a power of two because I
like powers of two.

## Coordination server

The coordination server exposes a websocket endpoint, where clients can connect to register themselves to a
topic and receive updates when the list of peers for that topic changes.

Clients should connect to `${baseUrl}/websocket?topic=TOPIC&name=NAME&ip=IP&port=PORT`, changing the placeholder
values to real ones. Every time the peer list changes, the server will send a websockett text message containing
an object in the format below.

If the websocket connection is closed, the server will send an update to all other peers removing the disconnected
peer.

The `last_seen` value can be updated by sending websocket ping frames.

```
{
    "peers": [
        {
            "ip": "1.1.1.1",
            "port": 6969,
            "name": "cloudflare",
            "last_seen": 1656829882876
        },
        {
            "ip": "8.8.8.8",
            "port": 4242,
            "name": "google",
            "last_seen": 1656829899576
        }
    ]
}
```

