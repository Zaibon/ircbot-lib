package ircbot

import (
	"bufio"
	"crypto/rand"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/textproto"
	"strings"
	"time"
)

type IrcBot struct {
	// identity
	User string
	Nick string

	// server info
	Server  string
	Port    string
	Channel []string

	// tcp communication
	conn   net.Conn
	reader *textproto.Reader
	writer *textproto.Writer

	// web interface
	WebEnable bool
	WebPort   string

	// crypto
	Encrypted bool
	config    tls.Config

	// data flow
	In    chan *IrcMsg
	Out   chan *IrcMsg
	Error chan error

	// exit flag
	Exit chan bool

	//action handlers
	Handlers map[string][]ActionFunc

	//are we Joined in channel?
	Joined bool
}

func NewIrcBot() *IrcBot {
	bot := IrcBot{
		Handlers: make(map[string][]ActionFunc),
		In:       make(chan *IrcMsg),
		Out:      make(chan *IrcMsg),
		Error:    make(chan error),
		Exit:     make(chan bool),
		Joined:   false,
	}

	//defautl actions, needed to run proprely
	bot.AddAction("PING", Pong)
	bot.AddAction("MODE", ValidConnect)

	return &bot
}

func (b *IrcBot) url() string {
	return fmt.Sprintf("%s:%s", b.Server, b.Port)
}

func (b *IrcBot) Connect() {
	//launch a go routine that handle errors
	// b.handleError()

	log.Println("Info> connection to", b.url())

	var tcpCon net.Conn
	var err error
	if b.Encrypted {
		cert, err := tls.LoadX509KeyPair("cert.pem", "key.pem")
		b.errChk(err)

		config := tls.Config{Certificates: []tls.Certificate{cert}}
		config.Rand = rand.Reader
		tcpCon, err = tls.Dial("tcp", b.url(), &config)
		b.errChk(err)

	} else {
		tcpCon, err = net.Dial("tcp", b.url())
		b.errChk(err)
	}

	b.conn = tcpCon
	r := bufio.NewReader(b.conn)
	w := bufio.NewWriter(b.conn)
	b.reader = textproto.NewReader(r)
	b.writer = textproto.NewWriter(w)

	//connect to server
	b.writer.PrintfLine("USER %s 8 * :%s", b.Nick, b.Nick)
	b.writer.PrintfLine("NICK %s", b.Nick)

	//launch go routines that handle requests
	b.listen()
	b.handleActionIn()
	b.handleActionOut()
	b.HandleError()
	if b.WebEnable {
		b.HandleWeb()
	}

	//join all channels
	b.join()
}

func (b *IrcBot) join() {

	//prevent to send JOIN command before we are conected
	for {
		if !b.Joined {
			time.Sleep(1 * time.Second)
			continue
		}
		break
	}

	for _, v := range b.Channel {
		s := fmt.Sprintf("JOIN %s", v)
		fmt.Println("irc >> ", s)
		b.writer.PrintfLine(s)
	}
	b.Joined = true
}

func (b *IrcBot) listen() {

	go func() {

		for {
			//block read line from socket
			line, err := b.reader.ReadLine()
			if err != nil {
				b.Error <- err
			}
			//convert line into IrcMsg
			msg := Parseline(line)
			b.In <- msg
		}

	}()
}

func (b *IrcBot) Say(s string) {
	msg := NewIrcMsg()
	msg.Command = "PRIVMSG"
	msg.Args = append(msg.Args, s)

	b.Out <- msg
}

func (b *IrcBot) AddAction(command string, action ActionFunc) {
	b.Handlers[command] = append(b.Handlers[command], action)
}

func (b *IrcBot) handleActionIn() {
	go func() {
		for {
			//receive new message
			msg := <-b.In
			fmt.Println("irc << ", msg.Raw)
			//handle action
			actions := b.Handlers[msg.Command]
			if len(actions) > 0 {
				for _, action := range actions {
					action(b, msg)
				}
			}
		}
	}()
}

func (b *IrcBot) handleActionOut() {
	go func() {
		for {
			msg := <-b.Out

			//we send nothing before we sure we join channel
			if b.Joined == false {
				continue
			}

			s := fmt.Sprintf("%s %s %s", msg.Command, msg.Channel, strings.Join(msg.Args, " "))
			fmt.Println("irc >> ", s)
			b.writer.PrintfLine(s)
		}
	}()
}

func (b *IrcBot) HandleError() {
	go func() {
		for {
			err := <-b.Error
			fmt.Printf("error >> %s", err)
			if err != nil {
				b.Disconnect()
				log.Fatalln("Error ocurs :", err)
			}
		}
	}()
}

//HandleWeb handles requests receive on http server
func (b *IrcBot) HandleWeb() {
	go func() {
		http.HandleFunc("/qg", Gui)
		http.HandleFunc("/send", func(w http.ResponseWriter, r *http.Request) {
			Send(b, w, r)
		})
		http.HandleFunc("/ircbot", func(w http.ResponseWriter, r *http.Request) {
			Handler(b, w, r)
		})
		http.ListenAndServe(":"+b.WebPort, nil)
	}()
}

func (b *IrcBot) errChk(err error) {
	if err != nil {
		log.Println("Error> ", err)
		b.Error <- err
	}
}

func (b *IrcBot) Disconnect() {
	b.writer.PrintfLine("QUIT")
	b.conn.Close()
}

func (b *IrcBot) String() string {
	s := fmt.Sprintf("server: %s\n", b.Server)
	s += fmt.Sprintf("port: %s\n", b.Port)
	s += fmt.Sprintf("ssl: %t\n", b.Encrypted)

	if len(b.Channel) > 0 {
		s += "channels: "
		for _, v := range b.Channel {
			s += fmt.Sprintf("%s ", v)
		}
	}

	return s
}
