package stub

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"strings"
)

// Registries is map os registry names => map domains => whoIsResult
// Read the stub file on init and create a registry stub server.
func init() {

	c, err := ioutil.ReadFile("stub.json")
	if err != nil {
		return
	}
	s, _ := NewServer("tcp", ":4443")
	go s.Run()
	err = json.Unmarshal(c, &Registries)
	if err != nil {
		panic(err)
	}
}

var Registries = map[string]map[string]string{}

type TestClient struct {
	Dialer *net.Dialer
	Client *http.Client
}

// this is a stub dialer. Function will pipe connections to the stub backend.
// function will read the payload from one end and write it to the other end.
func (client TestClient) Dial(network, address string) (net.Conn, error) {
	c, err := client.Dialer.Dial(network, ":4443")
	ca, b := net.Pipe()
	if err != nil {
		panic(err)
	}
	rw := bufio.NewReadWriter(bufio.NewReader(b), bufio.NewWriter(b))
	go func() {
		req, err := rw.ReadString('\n')
		if err != nil {
			panic(err)
		}
		c.Write([]byte(address + ";" + req + "\n"))
		buf := make([]byte, 0, 4096) // big buffer
		tmp := make([]byte, 256)     // using small tmo buffer for demonstrating
		for {
			n, err := c.Read(tmp)
			if err != nil {
				if err != io.EOF {
					fmt.Println("read error:", err)
				}
				break
			}
			buf = append(buf, tmp[:n]...)
		}
		rw.Write(buf)
		rw.Flush()
		b.Close()

	}()
	return ca, nil
}

// Server defines the Server the stubbed registries run on.
// TCP and UDP server implementations must satisfy.
type Server interface {
	Run() error
	Close() error
}

// NewServer creates a new Server using given protocol
// and addr.
func NewServer(protocol, addr string) (Server, error) {
	switch strings.ToLower(protocol) {
	case "tcp":
		return &TCPServer{
			addr: addr,
		}, nil
	case "udp":
		return &UDPServer{
			addr: addr,
		}, nil
	}
	return nil, errors.New("Invalid protocol given")
}

// TCPServer holds the structure of our TCP
// implementation.
type TCPServer struct {
	addr   string
	server net.Listener
}

// Run starts the TCP Server.
func (t *TCPServer) Run() (err error) {
	t.server, err = net.Listen("tcp", t.addr)
	if err != nil {
		return err
	}
	defer t.Close()

	go t.handleConnections()
	select {}
}

// Close shuts down the TCP Server
func (t *TCPServer) Close() (err error) {
	return t.server.Close()
}

// handleConnections is used to accept connections on
// the TCPServer and handle each of them in separate
// goroutines.
func (t *TCPServer) handleConnections() (err error) {
	for {
		conn, err := t.server.Accept()
		if err != nil || conn == nil {
			err = errors.New("could not accept connection")
			break
		}
		go t.handleConnection(conn)
	}
	return
}

// handleConnections deals with the business logic of
// each connection and their requests. This function will read the registry response based on the piped request from Dial function.
func (t *TCPServer) handleConnection(conn net.Conn) {
	defer conn.Close()

	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	for {
		req, err := rw.ReadString('\n')
		if err != nil {
			rw.WriteString("failed to read input")
			rw.Flush()
			return
		}
		r := strings.Split(req, ";")
		// remove new line
		r[1] = r[1][:len(r[1])-2]

		response := Registries[r[0]][r[1]]
		rw.Write([]byte(response))
		rw.Write([]byte("\n"))
		rw.Flush()

		conn.Close()
		return
	}
}

// UDPServer holds the necessary structure for our
// UDP server.
type UDPServer struct {
	addr   string
	server *net.UDPConn
}

// Run starts the UDP server.
func (u *UDPServer) Run() (err error) {
	laddr, err := net.ResolveUDPAddr("udp", u.addr)
	if err != nil {
		return errors.New("could not resolve UDP addr")
	}

	u.server, err = net.ListenUDP("udp", laddr)
	if err != nil {
		return errors.New("could not listen on UDP")
	}

	return u.handleConnections()
}

func (u *UDPServer) handleConnections() error {
	var err error
	for {
		buf := make([]byte, 2048)
		n, conn, err := u.server.ReadFromUDP(buf)
		if err != nil {
			log.Println(err)
			break
		}
		if conn == nil {
			continue
		}

		go u.handleConnection(conn, buf[:n])
	}
	return err
}

func (u *UDPServer) handleConnection(addr *net.UDPAddr, cmd []byte) {
	u.server.WriteToUDP([]byte(fmt.Sprintf("Request recieved: %s", cmd)), addr)
}

// Close ensures that the UDPServer is shut down gracefully.
func (u *UDPServer) Close() error {
	return u.server.Close()
}
