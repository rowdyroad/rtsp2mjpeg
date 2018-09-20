package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

type stream struct {
	DataChan chan []byte
	DoneChan chan bool
}

//NewCaster creates Caster instance
func NewCaster() *Caster {
	return &Caster{sync.Mutex{}, map[string]*[]stream{}}
}

//Caster converting rtsp(or any) stream to mjpeg (with ffmjpeg support)
type Caster struct {
	sync.Mutex
	streams map[string]*[]stream
}

func (c *Caster) Close() {
	c.Lock()
	defer c.Unlock()

	for _, streams := range c.streams {
		for _, stream := range *streams {
			stream.DoneChan <- true
		}
	}
}

//Cast main functionality for convert rtsp(or any) to mjpeg
func (c *Caster) Cast(command map[string]string, stopChan <-chan bool) (chan []byte, chan bool, error) {
	c.Lock()
	defer c.Unlock()

	id := ""
	for name, value := range command {
		id += name + "=" + value + ";"
	}

	source, has := command["source"]
	if !has {
		return nil, nil, errors.New("source attribute is required")
	}
	fps, _ := strconv.ParseInt(command["fps"], 10, 64)
	qscale, _ := strconv.ParseInt(command["qscale"], 10, 64)
	scale, _ := command["scale"]

	log.Println("Casting for", id)

	streams, exists := c.streams[id]
	if !exists {
		c.streams[id] = &[]stream{}
		streams = c.streams[id]
	}
	current := stream{make(chan []byte), make(chan bool)}
	*streams = append(*streams, current)

	stop := false

	go func() {
		<-stopChan
		log.Println("Client gone")
		c.Lock()
		defer c.Unlock()
		for index, cts := range *streams {
			if cts == current {
				log.Println("Removing client stream record.")
				*streams = append((*streams)[:index], (*streams)[index+1:]...)
				break
			}
		}
		if len(*streams) == 0 {
			log.Println("All clients gone. Stop casting.")
			delete(c.streams, id)
			stop = true
		}
	}()

	if !exists {
		log.Println("No active stream for source. Creating.")
		go func() {
			defer func() {
				log.Println("Done broadcasting")
				c.Lock()
				defer c.Unlock()
				for _, cts := range *streams {
					cts.DoneChan <- true
				}
			}()

			log.Println("Running ffmpeg for", id)

			execCommand := "ffmpeg -i " + source + " -c:v mjpeg -f mjpeg"
			if fps > 0 {
				execCommand += fmt.Sprintf(" -r %d ", fps)
			}
			if qscale > 0 {
				execCommand += fmt.Sprintf(" -q:v %d ", qscale)
			}

			if len(scale) > 0 {
				execCommand += fmt.Sprintf(" -vf 'scale=%s' ", strings.Replace(scale, "'", "\\'", -1))
			}
			log.Println("Exec command:", execCommand)
			cmd := exec.Command("bash", "-c", execCommand+" - 2>/dev/null")
			stdout, err := cmd.StdoutPipe()

			if err != nil {
				log.Println("Error:", err)
				return
			}
			if err := cmd.Start(); err != nil {
				log.Println("Error:", err)
				return
			}
			buf := make([]byte, 512*1024)

			for !stop {
				n, err := stdout.Read(buf)
				if n == 0 || (err != nil && err != io.EOF) {
					log.Println("Error:", err)
					return
				}

				c.Lock()
				for _, cts := range *streams {
					cts.DataChan <- buf[:n]
				}
				c.Unlock()
			}
		}()
	} else {
		log.Println("Source exists. Attaching.")
	}

	return current.DataChan, current.DoneChan, nil
}