package baichuan

import (
	"context"
	"fmt"
)

// Custom siren sound upload, as the Reolink app does it (captured
// 2026-10-26): import audio (261) carrying a header document with the file
// size and format, then the file itself in 2048-byte binary chunks on 261
// under the same message number, each answered 200, then save audio (262).
// The file is raw IMA ADPCM at 16 kHz (adpcm.EncodeStream).
const soundChunk = 2048

// UploadSound sends adpcm as the camera's custom siren sound and saves it.
func UploadSound(ctx context.Context, conn *Conn, adpcm []byte) error {
	if len(adpcm) == 0 {
		return fmt.Errorf("baichuan: empty sound")
	}
	ch := conn.opts.Channel
	ext, err := channelXML(ch)
	if err != nil {
		return err
	}
	info := []byte(fmt.Sprintf("%s\n<body>\n<audioFileInfo version=\"1.1\">\n<channelId>%d</channelId>\n<fileSize>%d</fileSize>\n<fileFormat>adpcm</fileFormat>\n</audioFileInfo>\n</body>\n", cameraXMLHeader, ch, len(adpcm)))
	h := Header{MsgID: MsgIDImportAudio, Class: ClassModern24, EncOffset: EncOffsetFor(byte(ch), 0, conn.nextCounter(), 0)}
	if err := conn.w.WriteParts(h, ext, info); err != nil {
		return err
	}
	if err := awaitOK(ctx, conn, MsgIDImportAudio, "sound header"); err != nil {
		return err
	}
	data, err := talkDataXML(ch)
	if err != nil {
		return err
	}
	for off := 0; off < len(adpcm); off += soundChunk {
		end := min(off+soundChunk, len(adpcm))
		if err := conn.w.WriteMedia(h, data, adpcm[off:end]); err != nil {
			return err
		}
		if err := awaitOK(ctx, conn, MsgIDImportAudio, fmt.Sprintf("sound bytes %d-%d", off, end)); err != nil {
			return err
		}
	}
	save := Header{MsgID: MsgIDSaveAudio, Class: ClassModern24, EncOffset: EncOffsetFor(byte(ch), 0, conn.nextCounter(), 0), PayloadOff: uint32(len(ext))}
	if err := conn.w.Write(save, ext); err != nil {
		return err
	}
	return awaitOK(ctx, conn, MsgIDSaveAudio, "saving the sound")
}

func awaitOK(ctx context.Context, conn *Conn, id uint32, what string) error {
	_, status, err := awaitReply(ctx, conn, id)
	if err != nil {
		return fmt.Errorf("baichuan: %s: %w", what, err)
	}
	if status != 200 {
		return fmt.Errorf("baichuan: %s: %s", what, ExplainStatus(status))
	}
	return nil
}
