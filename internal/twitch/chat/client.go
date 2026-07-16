package chat

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"
)

type Client struct {
	Username  string
	Token     string
	Channel   string
	Logger    *slog.Logger
	OnMention func(sender, message string)

	Addr string

	// Dial, when set, replaces the direct TCP dial — e.g. to tunnel the IRC
	// connection through the same SOCKS5 proxy as the HTTP clients. Nil
	// means a direct connection.
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)

	conn net.Conn
	mu   sync.Mutex
}

func NewClient(username, token, channel string, logger *slog.Logger, onMention func(sender, message string)) *Client {
	return &Client{
		Username:  strings.ToLower(username),
		Token:     token,
		Channel:   strings.ToLower(channel),
		Logger:    logger,
		OnMention: onMention,
	}
}

func (c *Client) Run(ctx context.Context) error {
	addr := c.Addr
	if addr == "" {
		addr = "irc.chat.twitch.tv:6697"
	}
	dial := c.Dial
	if dial == nil {
		dial = (&net.Dialer{Timeout: 10 * time.Second}).DialContext
	}

	dialCtx, cancelDial := context.WithTimeout(ctx, 10*time.Second)
	defer cancelDial()

	conn, err := dial(dialCtx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	if strings.Contains(addr, "irc.chat.twitch.tv") || strings.HasSuffix(addr, ":6697") {
		host, _, splitErr := net.SplitHostPort(addr)
		if splitErr != nil {
			host = addr
		}
		tlsConn := tls.Client(conn, &tls.Config{ServerName: host})
		if err := tlsConn.HandshakeContext(dialCtx); err != nil {
			conn.Close()
			return fmt.Errorf("tls handshake: %w", err)
		}
		conn = tlsConn
	}

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		if c.conn != nil {
			c.conn.Close()
		}
		c.mu.Unlock()
	}()

	pass := fmt.Sprintf("PASS oauth:%s\r\n", c.Token)
	nick := fmt.Sprintf("NICK %s\r\n", c.Username)
	join := fmt.Sprintf("JOIN #%s\r\n", c.Channel)

	if _, err := conn.Write([]byte(pass + nick + join)); err != nil {
		return fmt.Errorf("write handshake: %w", err)
	}

	c.Logger.Info("joined chat presence", slog.String("channel", c.Channel))

	errChan := make(chan error, 1)
	go func() {
		reader := bufio.NewReader(conn)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				errChan <- err
				return
			}
			line = strings.TrimSpace(line)

			if strings.HasPrefix(line, "PING ") {
				pong := strings.Replace(line, "PING", "PONG", 1) + "\r\n"
				c.mu.Lock()
				if c.conn != nil {
					c.conn.Write([]byte(pong))
				}
				c.mu.Unlock()
				continue
			}

			if strings.Contains(line, " PRIVMSG ") {
				c.handlePrivMsg(line)
			}
		}
	}()

	select {
	case <-ctx.Done():
		part := fmt.Sprintf("PART #%s\r\n", c.Channel)
		c.mu.Lock()
		if c.conn != nil {
			c.conn.Write([]byte(part))
		}
		c.mu.Unlock()
		return ctx.Err()
	case err := <-errChan:
		return err
	}
}

func (c *Client) handlePrivMsg(line string) {
	parts := strings.SplitN(line, " PRIVMSG ", 2)
	if len(parts) < 2 {
		return
	}
	prefix := parts[0]
	rest := parts[1]

	nick := ""
	if strings.HasPrefix(prefix, ":") {
		nickPart := strings.SplitN(prefix[1:], "!", 2)
		nick = nickPart[0]
	}

	msgParts := strings.SplitN(rest, " :", 2)
	if len(msgParts) < 2 {
		return
	}
	msg := msgParts[1]

	lowerMsg := strings.ToLower(msg)
	mention := "@" + c.Username
	if strings.Contains(lowerMsg, mention) || strings.Contains(lowerMsg, c.Username) {
		c.Logger.Info("chat mention detected",
			slog.String("channel", c.Channel),
			slog.String("from", nick),
			slog.String("message", msg),
		)
		if c.OnMention != nil {
			c.OnMention(nick, msg)
		}
	}
}
