package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/go-audio/audio"
	"github.com/go-audio/wav"
	"github.com/gordonklaus/portaudio"
	"gitlab.com/gomidi/midi/v2"
	_ "gitlab.com/gomidi/midi/v2/drivers/rtmididrv" // autoregisters driver
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
	format := mooDec.Format()
	fmt.Println(format.NumChannels, format.SampleRate)

	out := make([]int32, 8192)
	defaultDevice, err := portaudio.DefaultOutputDevice()
	if err != nil {
		fmt.Println("Failed to open the default output device:", err)
		os.Exit(1)
	}
	fmt.Println("Default output device:", defaultDevice.Name)

	fmt.Printf("Opening audio device outputs: %d, sample rate: %d\n", mooDec.NumChans, sampleRate)
	stream, err := portaudio.OpenDefaultStream(0, int(mooDec.NumChans), float64(sampleRate), len(out), &out)
	check(err)
	defer stream.Close()

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
			fmt.Printf("starting note %s on channel %v with velocity %v\n", midi.Note(key), ch, vel)
			playAudioFile(mooDec, stream, out, &sig)
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

	time.Sleep(time.Second * 15)

	stop()
}

func playAudioFile(dec *wav.Decoder, stream *portaudio.Stream, out []int32, sig *chan (os.Signal)) {
	check(stream.Start())

	defer stream.Stop()
	buf := &audio.IntBuffer{Format: dec.Format(), Data: make([]int, len(out))}
	n, err := dec.PCMBuffer(buf)
	//fmt.Println(dec.BitDepth)
	// Assuming 32bit audio for now
	for i := range buf.Data {
		out[i] = int32(buf.Data[i])
	}
	check(stream.Write())
	for n > 0 && err == nil {
		n, err = dec.PCMBuffer(buf)
		// convert buf to a slice of int32 values
		for i := range buf.Data {
			out[i] = int32(buf.Data[i])
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
