package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
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

	mooF, err := os.Open("sounds/cow_moo_32b.wav")
	if err != nil {
		log.Fatal(err)
	}
	defer mooF.Close()
	mooDec := wav.NewDecoder(mooF)
	if !mooDec.IsValidFile() { // ReadInfo()
		fmt.Println("Not a valid WAV file")
		os.Exit(1)
	}
	sampleRate := mooDec.SampleRate
	out := make([]int32, 8192)

	// -------------
	mooFFart, err := os.Open("sounds/cow_fart_32b.wav")
	if err != nil {
		log.Fatal(err)
	}
	defer mooF.Close()
	mooFartDec := wav.NewDecoder(mooFFart)
	if !mooFartDec.IsValidFile() { // ReadInfo()
		fmt.Println("Not a valid cow fart WAV file")
		os.Exit(1)
	}
	fartSampleRate := mooFartDec.SampleRate
	out2 := make([]int32, 8192)

	// -------------
	defaultDevice, err := portaudio.DefaultOutputDevice()
	if err != nil {
		fmt.Println("Failed to open the default output device:", err)
		os.Exit(1)
	}
	fmt.Println("Default output device:", defaultDevice.Name)

	fmt.Printf("Opening stream outputs: %d, sample rate: %d\n", mooDec.NumChans, sampleRate)
	stream, err := portaudio.OpenDefaultStream(0, int(mooDec.NumChans), float64(sampleRate), len(out), &out)
	check(err)
	defer stream.Close()
	fmt.Printf("Opening another stream outputs: %d, sample rate: %d\n", mooFartDec.NumChans, fartSampleRate)
	stream2, err := portaudio.OpenDefaultStream(0, int(mooFartDec.NumChans), float64(fartSampleRate), len(out2), &out2)
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
				go func() { playAudioFile(mooDec, stream, &out, &sig, &streamMutex) }()
			}
			// A3 - Pad 2
			if key == 45 {
				go func() { playAudioFile(mooFartDec, stream2, &out2, &sig, &stream2Mutex) }()
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

func playAudioFile(dec *wav.Decoder, stream *portaudio.Stream, out *[]int32, sig *chan (os.Signal), mu *sync.Mutex) {
	fmt.Println("trying to play something")
	mu.Lock()
	defer mu.Unlock()
	check(stream.Start())

	defer stream.Stop()

	buf := &audio.IntBuffer{Format: dec.Format(), Data: make([]int, len(*out))}
	n, err := dec.PCMBuffer(buf)
	//fmt.Println(dec.BitDepth)
	// Assuming 32bit audio for now
	streamBuf := *out
	for i := range buf.Data {
		streamBuf[i] = int32(buf.Data[i])
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
		n, err = dec.PCMBuffer(buf)
		// convert buf to a slice of int32 values
		for i := range buf.Data {
			streamBuf[i] = int32(buf.Data[i])
		}
		check(stream.Write())
		select {
		case <-*sig:
			return
		default:
		}
	}
	dec.Seek(0, io.SeekStart)
	dec.FwdToPCM()
	if err != nil {
		fmt.Println("failed to read the PCM buffer", err)
	} else {
		dec.Rewind()
	}

}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
