package chat

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"
)

func TestClientMentionDetection(t *testing.T) {
	// Start local mock IRC server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer listener.Close()

	mentionChan := make(chan [2]string, 2)
	onMention := func(sender, message string) {
		mentionChan <- [2]string{sender, message}
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := NewClient("myuser", "mytoken", "streamerchannel", logger, onMention)
	client.Addr = listener.Addr().String()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		// Read handshake
		reader := bufio.NewReader(conn)
		for i := 0; i < 3; i++ {
			_, _ = reader.ReadString('\n')
		}

		// Simulate incoming messages
		time.Sleep(50 * time.Millisecond)
		conn.Write([]byte("PING :tmi.twitch.tv\r\n"))
		conn.Write([]byte(":anotheruser!anotheruser@anotheruser.tmi.twitch.tv PRIVMSG #streamerchannel :hello @myuser how are you?\r\n"))
		conn.Write([]byte(":someother!someother@someother.tmi.twitch.tv PRIVMSG #streamerchannel :just talking about myuser without tag\r\n"))
		conn.Write([]byte(":someother!someother@someother.tmi.twitch.tv PRIVMSG #streamerchannel :unrelated message\r\n"))
		time.Sleep(200 * time.Millisecond)
	}()

	go func() {
		_ = client.Run(ctx)
	}()

	select {
	case mention := <-mentionChan:
		if mention[0] != "anotheruser" || mention[1] != "hello @myuser how are you?" {
			t.Errorf("unexpected first mention: %v", mention)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for first mention")
	}

	select {
	case mention := <-mentionChan:
		if mention[0] != "someother" || mention[1] != "just talking about myuser without tag" {
			t.Errorf("unexpected second mention: %v", mention)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for second mention")
	}
}
