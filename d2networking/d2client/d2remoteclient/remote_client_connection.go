// Package d2remoteclient facilitates communication between a remote client and server.
package d2remoteclient

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strings"

	"github.com/OpenDiablo2/OpenDiablo2/d2networking/d2client/d2clientconnectiontype"

	"github.com/OpenDiablo2/OpenDiablo2/d2game/d2player"

	"github.com/OpenDiablo2/OpenDiablo2/d2networking"
	"github.com/OpenDiablo2/OpenDiablo2/d2networking/d2netpacket"
	"github.com/OpenDiablo2/OpenDiablo2/d2networking/d2netpacket/d2netpackettype"
	uuid "github.com/satori/go.uuid"
)

// RemoteClientConnection is the implementation of ClientConnection
// for a remote client.
type RemoteClientConnection struct {
	clientListener d2networking.ClientListener // The GameClient
	uniqueId       string                      // Unique ID generated on construction
	udpConnection  *net.UDPConn                // UDP connection to the server
	active         bool                        // The connection is currently open
}

// GetUniqueId returns RemoteClientConnection.uniqueId.
func (l RemoteClientConnection) GetUniqueId() string {
	return l.uniqueId
}

// GetConnectionType returns an enum representing the connection type.
// See: d2clientconnectiontype
func (l RemoteClientConnection) GetConnectionType() d2clientconnectiontype.ClientConnectionType {
	return d2clientconnectiontype.LANClient
}

// SendPacketToClient passes a packet to the game client for processing.
func (l *RemoteClientConnection) SendPacketToClient(packet d2netpacket.NetPacket) error {
	return l.clientListener.OnPacketReceived(packet)
}

// Create constructs a new RemoteClientConnection
// and returns a pointer to it.
func Create() *RemoteClientConnection {
	result := &RemoteClientConnection{
		uniqueId: uuid.NewV4().String(),
	}

	return result
}

// Open runs serverListener() in a goroutine to continuously read UDP packets.
// It also sends a PlayerConnectionRequestPacket packet to the server (see d2netpacket).
func (l *RemoteClientConnection) Open(connectionString string, saveFilePath string) error {
	if !strings.Contains(connectionString, ":") {
		connectionString += ":6669"
	}

	// TODO: Connect to the server
	udpAddress, err := net.ResolveUDPAddr("udp", connectionString)

	// TODO: Show connection error screen if connection fails
	if err != nil {
		return err
	}

	l.udpConnection, err = net.DialUDP("udp", nil, udpAddress)
	// TODO: Show connection error screen if connection fails
	if err != nil {
		return err
	}

	l.active = true
	go l.serverListener()

	log.Printf("Connected to server at %s", l.udpConnection.RemoteAddr().String())
	gameState := d2player.LoadPlayerState(saveFilePath)
	err = l.SendPacketToServer(d2netpacket.CreatePlayerConnectionRequestPacket(l.GetUniqueId(), gameState))
	if err != nil {
		log.Print("RemoteClientConnection: error sending PlayerConnectionRequestPacket to server.")
		return err
	}

	return nil
}

// Close informs the server that this client has disconnected and sets
// RemoteClientConnection.active to false.
func (l *RemoteClientConnection) Close() error {
	l.active = false
	err := l.SendPacketToServer(d2netpacket.CreatePlayerDisconnectRequestPacket(l.GetUniqueId()))
	if err != nil {
		return err
	}

	return nil
}

// SendPacketToServer compresses the JSON encoding of a NetPacket and
// sends it to the server.
func (l *RemoteClientConnection) SendPacketToServer(packet d2netpacket.NetPacket) error {
	data, err := json.Marshal(packet.PacketData)
	if err != nil {
		return err
	}
	var buff bytes.Buffer
	buff.WriteByte(byte(packet.PacketType))
	writer, _ := gzip.NewWriterLevel(&buff, gzip.BestCompression)

	if written, err := writer.Write(data); err != nil {
		return err
	} else if written == 0 {
		return errors.New(fmt.Sprintf("RemoteClientConnection: attempted to send empty %v packet body.", packet.PacketType))
	}
	if err = writer.Close(); err != nil {
		return err
	}
	if _, err = l.udpConnection.Write(buff.Bytes()); err != nil {
		return err
	}
	return nil
}

// SetClientListener sets RemoteClientConnection.clientListener to the given value.
func (l *RemoteClientConnection) SetClientListener(listener d2networking.ClientListener) {
	l.clientListener = listener
}

// serverListener runs a while loop, reading from the GameServer's UDP
// connection.
func (l *RemoteClientConnection) serverListener() {
	buffer := make([]byte, 4096)
	for l.active {
		n, _, err := l.udpConnection.ReadFromUDP(buffer)
		if err != nil {
			fmt.Printf("Socket error: %s\n", err)
			continue
		}
		if n <= 0 {
			continue
		}
		buff := bytes.NewBuffer(buffer)
		packetTypeId, err := buff.ReadByte()
		packetType := d2netpackettype.NetPacketType(packetTypeId)
		reader, err := gzip.NewReader(buff)
		sb := new(strings.Builder)
		written, err := io.Copy(sb, reader)
		if err != nil {
			log.Printf("RemoteClientConnection: error copying bytes from %v packet: %s", packetType, err)
			// TODO: All packets coming from the client seem to be throwing an error
			//continue
		}
		if written == 0 {
			log.Printf("RemoteClientConnection: empty packet %v packet received", packetType)
			continue
		}

		stringData := sb.String()
		switch packetType {
		case d2netpackettype.GenerateMap:
			var packet d2netpacket.GenerateMapPacket
			err := json.Unmarshal([]byte(stringData), &packet)
			if err != nil {
				log.Printf("GameServer: error unmarshalling %T: %s", packet, err)
				continue
			}
			err = l.SendPacketToClient(d2netpacket.NetPacket{
				PacketType: packetType,
				PacketData: packet,
			})
			if err != nil {
				log.Printf("RemoteClientConnection: error processing packet %v: %s", packetType, err)
			}
		case d2netpackettype.MovePlayer:
			var packet d2netpacket.MovePlayerPacket
			err := json.Unmarshal([]byte(stringData), &packet)
			if err != nil {
				log.Printf("GameServer: error unmarshalling %T: %s", packet, err)
				continue
			}
			err = l.SendPacketToClient(d2netpacket.NetPacket{
				PacketType: packetType,
				PacketData: packet,
			})
			if err != nil {
				log.Printf("RemoteClientConnection: error processing packet %v: %s", packetType, err)
			}
		case d2netpackettype.UpdateServerInfo:
			var packet d2netpacket.UpdateServerInfoPacket
			err := json.Unmarshal([]byte(stringData), &packet)
			if err != nil {
				log.Printf("GameServer: error unmarshalling %T: %s", packet, err)
				continue
			}
			err = l.SendPacketToClient(d2netpacket.NetPacket{
				PacketType: packetType,
				PacketData: packet,
			})
			if err != nil {
				log.Printf("RemoteClientConnection: error processing packet %v: %s", packetType, err)
			}
		case d2netpackettype.AddPlayer:
			var packet d2netpacket.AddPlayerPacket
			err := json.Unmarshal([]byte(stringData), &packet)
			if err != nil {
				log.Printf("GameServer: error unmarshalling %T: %s", packet, err)
				continue
			}
			err = l.SendPacketToClient(d2netpacket.NetPacket{
				PacketType: packetType,
				PacketData: packet,
			})
			if err != nil {
				log.Printf("RemoteClientConnection: error processing packet %v: %s", packetType, err)
			}
		case d2netpackettype.Ping:
			err := l.SendPacketToServer(d2netpacket.CreatePongPacket(l.uniqueId))
			if err != nil {
				log.Printf("RemoteClientConnection: error responding to server ping: %s", err)
			}
		case d2netpackettype.PlayerDisconnectionNotification:
			var packet d2netpacket.PlayerDisconnectRequestPacket
			err := json.Unmarshal([]byte(stringData), &packet)
			if err != nil {
				log.Printf("GameServer: error unmarshalling %T: %s", packet, err)
				continue
			}
			log.Printf("Received disconnect: %s", packet.Id)
		default:
			fmt.Printf("Unknown packet type %d\n", packetType)
		}

	}
}
