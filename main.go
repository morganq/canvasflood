package main

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"bytes"
	"compress/gzip"
)

var width int = 800
var height int = 600

var stride = 4
var pixels = make([]uint8, width*height*stride)
var delta_pixels = make([]uint8, width*height*stride)

var packets int64 = 0
var avg_duration = 5
var last_time = time.Now()

func handleSet(x, y uint16, r, g, b uint8) {
	if x < 0 || int(x) >= width || y < 0 || int(y) >= height {
		return
	}
	mem_start := int(y)*width*stride + int(x)*stride
	pixels[mem_start] = r
	pixels[mem_start+1] = g
	pixels[mem_start+2] = b
	pixels[mem_start+3] = 255
	delta_pixels[mem_start] = r
	delta_pixels[mem_start+1] = g
	delta_pixels[mem_start+2] = b
	delta_pixels[mem_start+3] = 255
}

func handlePacket(msg string) {
	parts := strings.Split(msg, " ")
	if parts[0] == "set" {
		x, _ := strconv.ParseUint(parts[1], 10, 16)
		y, _ := strconv.ParseUint(parts[2], 10, 16)
		r, _ := strconv.ParseUint(parts[3], 10, 8)
		g, _ := strconv.ParseUint(parts[4], 10, 8)
		b, _ := strconv.ParseUint(parts[5], 10, 8)
		handleSet(uint16(x), uint16(y), uint8(r), uint8(g), uint8(b))
	}

	packets += 1
	timesince := time.Since(last_time).Seconds()
	if timesince > float64(avg_duration) {
		last_time = time.Now()
		fmt.Printf("Handling %.1f packets/sec\n", float64(packets)/timesince)
		packets = 0
	}
}

func udpserver() {
	conn, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("0.0.0.0"), Port: 1871})
	defer conn.Close()

	for {
		buf := make([]byte, 256)
		read_len, _, _ := conn.ReadFromUDP(buf)
		msg := string(buf[0:read_len])
		//fmt.Println(msg)
		go handlePacket(msg)
	}
}

var allConns []*websocket.Conn

func sendPixels(conn *websocket.Conn, pix []uint8) {
	var b bytes.Buffer
	w,_ := gzip.NewWriterLevel(&b, 9)
	w.Write([]byte(pix))
	w.Close()
	
	conn.WriteMessage(websocket.BinaryMessage, b.Bytes())
}

var wsupgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 4096,
}

func wshandler(w http.ResponseWriter, r *http.Request) {
	conn, err := wsupgrader.Upgrade(w, r, nil)

	sendPixels(conn, pixels)
	allConns = append(allConns, conn);

	if err != nil {
		fmt.Println("Failed to set websocket upgrade: %+v", err)
		return
	}

	for {
		t, msg, err := conn.ReadMessage()
		if err != nil {
			break
		}
		conn.WriteMessage(t, msg)
	}
}

func tcpserver() {
	r := gin.Default()
	r.LoadHTMLFiles("www/index.html")

	r.GET("/", func(c *gin.Context) {
		c.HTML(200, "index.html", nil)
	})

	r.GET("/delta", func(c *gin.Context) {
		wshandler(c.Writer, c.Request)
	})

	r.Run("0:8080")
}

func main() {

	for i := 0; i < width * height * stride ; i+=4 {
		pixels[i+3] = 255
	}

	ticker := time.NewTicker(time.Millisecond * 200)
	go func() {
		for {
			<-ticker.C
			fmt.Printf("sending deltas to %d clients\n", len(allConns));
			for _,conn := range allConns {
				sendPixels(conn, delta_pixels);	
			}
			delta_pixels = make([]uint8, width*height*stride)
		}
	}()

	var wg sync.WaitGroup
	wg.Add(2)
	go udpserver()
	go tcpserver()
	wg.Wait()
}
