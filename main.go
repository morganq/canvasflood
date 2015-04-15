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
	"encoding/binary"
)

var width int = 800
var height int = 600

var stride = 4
var pixels = make([]uint8, width*height*stride)
var delta_pixels = make([]uint8, width*height*stride)

var packets int64 = 0
var avg_duration = 5
var last_time = time.Now()

var delta_rect_x1 int = 800
var delta_rect_y1 int = 600
var delta_rect_x2 int = 0
var delta_rect_y2 int = 0

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
	if int(x) > delta_rect_x2 { delta_rect_x2 = int(x) }
	if int(x) < delta_rect_x1 { delta_rect_x1 = int(x) }
	if int(y) > delta_rect_y2 { delta_rect_y2 = int(y) }
	if int(y) < delta_rect_y1 { delta_rect_y1 = int(y) }
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
		go handlePacket(msg)
	}
}

var allConns []*websocket.Conn

func getCompressedPixels() []byte {
	pix_saved := make([]uint8, width*height*stride)
	copy(pix_saved, pixels)
	var b bytes.Buffer
	w,_ := gzip.NewWriterLevel(&b, 9)
	x1 := make([]byte, 2)
	y1 := make([]byte, 2)
	x2 := make([]byte, 2)
	y2 := make([]byte, 2)
	
	binary.LittleEndian.PutUint16(x1, uint16(0))
	binary.LittleEndian.PutUint16(y1, uint16(0))
	binary.LittleEndian.PutUint16(x2, uint16(width))
	binary.LittleEndian.PutUint16(y2, uint16(height))

	w.Write(x1)
	w.Write(y1)
	w.Write(x2)
	w.Write(y2)

	w.Write([]byte(pix_saved))
	w.Flush()
	w.Close()
	
	return b.Bytes()
}

func getDeltaCompressedPixels() []byte {
	w := delta_rect_x2 - delta_rect_x1
	h := delta_rect_y2 - delta_rect_y1
	if w < 1 || h < 1 {
		return nil
	}
	fmt.Printf("%d pixels to send (%dx%d)\n", w*h,w,h)
	pix_box := make([]uint8, w*h*stride)
	for y := 0; y < h; y++ {
		n_m := y * w * stride
		n_total := w * stride
		o_m := (delta_rect_y1 + y) * width * stride + delta_rect_x1 * stride
		row := delta_pixels[o_m:o_m+n_total]
		copy(pix_box[n_m:], row)
	}
	var b bytes.Buffer
	wr,_ := gzip.NewWriterLevel(&b, 9)

	x1 := make([]byte, 2)
	y1 := make([]byte, 2)
	x2 := make([]byte, 2)
	y2 := make([]byte, 2)
	
	binary.LittleEndian.PutUint16(x1, uint16(delta_rect_x1))
	binary.LittleEndian.PutUint16(y1, uint16(delta_rect_y1))
	binary.LittleEndian.PutUint16(x2, uint16(w))
	binary.LittleEndian.PutUint16(y2, uint16(h))

	wr.Write(x1)
	wr.Write(y1)
	wr.Write(x2)
	wr.Write(y2)
	wr.Write([]byte(pix_box))
	wr.Flush()
	wr.Close()
	
	return b.Bytes()
}

var wsupgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 4096,
}

func wshandler(w http.ResponseWriter, r *http.Request) {
	conn, err := wsupgrader.Upgrade(w, r, nil)

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
		tickIndex := 0
		for {
			<-ticker.C
			fmt.Printf("sending deltas to %d clients\n", len(allConns));
			var bytesToSend []byte
			if tickIndex % 20 == 0 {
				bytesToSend = getCompressedPixels()
			} else {
				bytesToSend = getDeltaCompressedPixels()
			}

			delta_pixels = make([]uint8, width*height*stride)
			delta_rect_x1 = 800
			delta_rect_x2 = 0
			delta_rect_y1 = 600
			delta_rect_y2 = 0

			if bytesToSend != nil {
				for _,conn := range allConns {
					conn.WriteMessage(websocket.BinaryMessage, bytesToSend)
				}
			}
			tickIndex++
		}
	}()

	var wg sync.WaitGroup
	wg.Add(2)
	go udpserver()
	go tcpserver()
	wg.Wait()
}
