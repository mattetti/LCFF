package main

import (
	"bytes"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/go-audio/audio"
	"github.com/go-audio/wav"
	"github.com/gordonklaus/portaudio"
	"gitlab.com/gomidi/midi/v2"
	_ "gitlab.com/gomidi/midi/v2/drivers/rtmididrv" // autoregisters driver
)

var (
	streamMutex  sync.Mutex
	stream2Mutex sync.Mutex
)

func main() {
	defer midi.CloseDriver()

	portaudio.Initialize()
	defer portaudio.Terminate()

	ports := midi.InPorts()
	if len(ports) == 0 {
		fmt.Println("No MIDI input ports available")
		os.Exit(1)
	}
	for id, port := range ports {
		if id == 0 {
			fmt.Printf("Listening to MIDI port %s\n", port)
		} else {
			fmt.Println(id, port)
		}
	}

	in := midi.FindInPort("Arturia BeatStep")
	if in < 0 {
		fmt.Println("can't find Arturia BeatStep")
		os.Exit(1)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, os.Kill)

	moo, err := NewSample("sounds/cow_moo_32b.wav")
	if err != nil {
		fmt.Printf("failed to load the cow sound - %v\n", err)
		moo.Close()
		os.Exit(1)
	}
	defer moo.Close()
	// out := make([]int32, 8192)

	// -------------
	fart, err := NewSample("sounds/cow_fart_32b.wav")
	if err != nil {
		fmt.Printf("failed to load the cow sound - %v\n", err)
		moo.Close()
		os.Exit(1)
	}
	defer fart.Close()
	// out2 := make([]int32, 8192)

	// -------------
	defaultDevice, err := portaudio.DefaultOutputDevice()
	if err != nil {
		fmt.Println("Failed to open the default output device:", err)
		os.Exit(1)
	}
	fmt.Println("Default output device:", defaultDevice.Name)

	stream, err := portaudio.OpenDefaultStream(0, int(moo.Decoder.NumChans), float64(moo.Decoder.SampleRate), len(moo.Buffer), &moo.Buffer)
	check(err)
	defer stream.Close()

	stream2, err := portaudio.OpenDefaultStream(0, int(fart.Decoder.NumChans), float64(fart.Decoder.SampleRate), len(fart.Buffer), &fart.Buffer)
	check(err)
	defer stream2.Close()

	stop, err := midi.ListenTo(in, func(msg midi.Message, timestampms int32) {
		var bt []byte
		var ch, key, vel uint8
		switch {
		case msg.GetSysEx(&bt):
			fmt.Printf("got sysex: % X\n", bt)
			// Stop control
			if bytes.Compare(bt, []byte{0x7F, 0x7F, 0x06, 0x01}) == 0 {
				os.Exit(0)
			}
		case msg.GetNoteStart(&ch, &key, &vel):

			fmt.Printf("starting note %d [%s] on channel %v with velocity %v\n", key, midi.Note(key), ch, vel)
			// Ab3 - Pad 1
			if key == 44 {
				go func() { playAudioFile(moo, stream, &sig) }()
			}
			// A3 - Pad 2
			if key == 45 {
				go func() { playAudioFile(fart, stream2, &sig) }()
			}
		case msg.GetNoteEnd(&ch, &key):
			fmt.Printf("ending note %s on channel %v\n", midi.Note(key), ch)
		default:
			// ignore
		}
	}, midi.UseSysEx())

	if err != nil {
		fmt.Printf("ERROR: %s\n", err)
		return
	}

	for {
		select {
		case <-sig:
			stop()
			return
		default:
		}
		time.Sleep(150 * time.Millisecond)
	}

}

func playAudioFile(sample *Sample, stream *portaudio.Stream, sig *chan (os.Signal)) {
	fmt.Println("trying to play something")
	sample.Mutex.Lock()
	defer sample.Mutex.Unlock()
	check(stream.Start())

	defer stream.Stop()

	buf := &audio.IntBuffer{Format: sample.Decoder.Format(), Data: make([]int, len(sample.Buffer))}
	n, err := sample.Decoder.PCMBuffer(buf)
	//fmt.Println(dec.BitDepth)
	// Assuming 32bit audio for now
	for i := range buf.Data {
		sample.Buffer[i] = int32(buf.Data[i])
	}
	if err = stream.Write(); err != nil {
		fmt.Println(err)
		time.Sleep(150 * time.Millisecond)
		if err = stream.Stop(); err != nil {
			fmt.Println(err)
			return
		}
		stream.Start()
		if err = stream.Write(); err != nil {
			fmt.Println(err)
			return
		}
	}

	for n > 0 && err == nil {
		n, err = sample.Decoder.PCMBuffer(buf)
		// convert buf to a slice of int32 values
		for i := range buf.Data {
			sample.Buffer[i] = int32(buf.Data[i])
		}
		check(stream.Write())
		select {
		case <-*sig:
			return
		default:
		}
	}
	if err != nil {
		fmt.Println("failed to read the PCM buffer", err)
	} else {
		sample.Decoder.Rewind()
	}

}

func check(err error) {
	if err != nil {
		panic(err)
	}
}

type Sample struct {
	file    *os.File
	Path    string
	Decoder *wav.Decoder
	Mutex   sync.Mutex
	Buffer  []int32
}

func (s *Sample) Close() {
	s.file.Close()
}

func NewSample(path string) (sample *Sample, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	sample = &Sample{
		file:    f,
		Path:    path,
		Decoder: wav.NewDecoder(f),
		Buffer:  make([]int32, 8192),
	}
	if !sample.Decoder.IsValidFile() {
		return sample, fmt.Errorf("Not a valid WAV file")
	}
	return sample, nil
}
