package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const outboxDDL = `CREATE TABLE IF NOT EXISTS outbox (
  id          UUID PRIMARY KEY,
  subject     TEXT NOT NULL,
  payload     JSONB NOT NULL,
  request_id  TEXT NOT NULL DEFAULT '',
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  sent_at     TIMESTAMPTZ
)`

const processedDDL = `CREATE TABLE IF NOT EXISTS processed_messages (
  consumer    TEXT NOT NULL,
  msg_id      TEXT NOT NULL,
  processed   TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (consumer, msg_id)
)`

// ConnectNATS membuka koneksi NATS dengan reconnect otomatis.
func ConnectNATS(url, name string) (*nats.Conn, jetstream.JetStream, error) {
	var nc *nats.Conn
	var err error
	for i := 0; i < 60; i++ {
		nc, err = nats.Connect(url, nats.Name(name), nats.MaxReconnects(-1), nats.ReconnectWait(time.Second))
		if err == nil {
			break
		}
		slog.Warn("menunggu nats", "err", err)
		time.Sleep(time.Second)
	}
	if err != nil {
		return nil, nil, err
	}
	js, err := jetstream.New(nc)
	return nc, js, err
}

// EnsureStream membuat stream SHOP (persisten di disk) bila belum ada. Duplicates window
// membuat JetStream membuang publish ulang dengan Nats-Msg-Id yang sama.
func EnsureStream(ctx context.Context, js jetstream.JetStream, name string, subjects []string) error {
	_, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name: name, Subjects: subjects, Storage: jetstream.FileStorage,
		Duplicates: 2 * time.Minute, MaxAge: 24 * time.Hour,
	})
	return err
}

// EnsureStreamRetention sama dengan EnsureStream tetapi dengan masa simpan dan batas ukuran
// sendiri. Dipakai stream BANK yang juga dibaca pipeline Data Engineering (perlu retensi lebih lama).
func EnsureStreamRetention(ctx context.Context, js jetstream.JetStream, name string, subjects []string,
	maxAge time.Duration, maxBytes int64) error {
	_, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name: name, Subjects: subjects, Storage: jetstream.FileStorage,
		Duplicates: 2 * time.Minute, MaxAge: maxAge, MaxBytes: maxBytes,
	})
	return err
}

// Envelope adalah bentuk payload event domain yang dibaca pipeline lain (kontrak data DE):
// event_id sama dengan id outbox dan dipakai sebagai header Nats-Msg-Id.
type Envelope struct {
	EventID    string    `json:"event_id"`
	EventType  string    `json:"event_type"`
	OccurredAt time.Time `json:"occurred_at"`
	Data       any       `json:"data"`
}

// EnqueueEvent menyimpan event berbentuk Envelope ke outbox. event_id = id outbox, sehingga
// relay menerbitkannya dengan Nats-Msg-Id = event_id (dedup di JetStream dan di consumer).
func EnqueueEvent(ctx context.Context, tx pgx.Tx, subject string, occurredAt time.Time, data any) (string, error) {
	id := NewID()
	b, err := json.Marshal(Envelope{EventID: id, EventType: subject, OccurredAt: occurredAt.UTC(), Data: data})
	if err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `INSERT INTO outbox (id, subject, payload, request_id) VALUES ($1,$2,$3,$4)`,
		id, subject, b, RequestID(ctx))
	return id, err
}

// Audit menerbitkan catatan aktivitas consumer ke subject audit.<service> (core NATS,
// at-most-once). Monitor memakainya untuk timeline; hilang satu tidak masalah.
func Audit(nc *nats.Conn, service string, fields map[string]any) {
	if nc == nil {
		return // mis. di unit test
	}
	fields["service"] = service
	if r := os.Getenv("REPLICA"); r != "" {
		fields["replica"] = r
	}
	fields["at"] = time.Now().UTC().Format(time.RFC3339Nano)
	b, _ := json.Marshal(fields)
	_ = nc.Publish("audit."+service, b)
}

// Enqueue menyimpan event ke tabel outbox di dalam transaksi bisnis yang sama
// (transactional outbox). Relay menerbitkannya ke JetStream kemudian.
func Enqueue(ctx context.Context, tx pgx.Tx, subject string, payload any) (string, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	id := NewID()
	_, err = tx.Exec(ctx, `INSERT INTO outbox (id, subject, payload, request_id) VALUES ($1,$2,$3,$4)`,
		id, subject, b, RequestID(ctx))
	return id, err
}

// RunRelay membaca outbox setiap 200 ms dan menerbitkan event ke JetStream. FOR UPDATE
// SKIP LOCKED membuat dua replica bisa menjalankan relay bersamaan tanpa mengirim ganda.
func RunRelay(ctx context.Context, pool *pgxpool.Pool, js jetstream.JetStream, log *slog.Logger, paused *atomic.Bool) {
	t := time.NewTicker(200 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if paused != nil && paused.Load() {
			continue
		}
		err := InTx(ctx, pool, func(tx pgx.Tx) error {
			rows, err := tx.Query(ctx, `SELECT id::text, subject, payload, request_id FROM outbox
				WHERE sent_at IS NULL ORDER BY created_at LIMIT 50 FOR UPDATE SKIP LOCKED`)
			if err != nil {
				return err
			}
			type row struct {
				id, subj, rid string
				data          []byte
			}
			var batch []row
			for rows.Next() {
				var r row
				if err := rows.Scan(&r.id, &r.subj, &r.data, &r.rid); err != nil {
					return err
				}
				batch = append(batch, r)
			}
			rows.Close()
			for _, r := range batch {
				m := nats.NewMsg(r.subj)
				m.Data = r.data
				m.Header.Set(HeaderRequestID, r.rid)
				pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
				_, err := js.PublishMsg(pctx, m, jetstream.WithMsgID(r.id))
				cancel()
				if err != nil {
					return fmt.Errorf("publish %s: %w", r.subj, err) // event tetap di outbox
				}
				if _, err := tx.Exec(ctx, `UPDATE outbox SET sent_at = now() WHERE id = $1`, r.id); err != nil {
					return err
				}
				log.Info("event diterbitkan dari outbox", "request_id", r.rid, "subject", r.subj, "msg_id", r.id)
			}
			return nil
		})
		if err != nil && ctx.Err() == nil {
			log.Warn("relay outbox gagal, dicoba lagi", "err", err)
			time.Sleep(time.Second)
		}
	}
}

// Delivery adalah satu pesan JetStream yang sedang diproses consumer.
type Delivery struct {
	Msg          jetstream.Msg
	Subject      string
	MsgID        string
	RequestID    string
	NumDelivered uint64
	Data         []byte
}

// ErrSkipAck dikembalikan handler untuk mensimulasikan crash sesudah commit tetapi
// sebelum ack. JetStream akan mengirim ulang pesan setelah AckWait habis.
var ErrSkipAck = errors.New("simulasi crash sebelum ack")

// ConsumerSpec menjelaskan durable pull consumer. Semua replica yang memakai nama
// durable yang sama berbagi pesan (setara queue group: satu pesan ke satu replica).
type ConsumerSpec struct {
	Stream  string
	Durable string
	Filters []string
	AckWait time.Duration
	Batch   int // jumlah pesan per fetch (bawaan 5)
}

// Consume menjalankan loop fetch-proses-ack dengan semantik at-least-once:
// proses dulu, baru ack. Handler error -> Nak dengan jeda (redelivery).
func Consume(ctx context.Context, js jetstream.JetStream, spec ConsumerSpec, log *slog.Logger,
	paused *atomic.Bool, handle func(ctx context.Context, d *Delivery) error) error {
	if spec.AckWait == 0 {
		spec.AckWait = 5 * time.Second
	}
	if spec.Batch == 0 {
		spec.Batch = 5
	}
	cons, err := js.CreateOrUpdateConsumer(ctx, spec.Stream, jetstream.ConsumerConfig{
		Durable: spec.Durable, FilterSubjects: spec.Filters, AckPolicy: jetstream.AckExplicitPolicy,
		AckWait: spec.AckWait, MaxDeliver: 20, DeliverPolicy: jetstream.DeliverAllPolicy,
	})
	if err != nil {
		return err
	}
	go func() {
		for ctx.Err() == nil {
			if paused != nil && paused.Load() {
				time.Sleep(300 * time.Millisecond)
				continue
			}
			batch, err := cons.Fetch(spec.Batch, jetstream.FetchMaxWait(time.Second))
			if err != nil {
				time.Sleep(500 * time.Millisecond)
				continue
			}
			for m := range batch.Messages() {
				meta, _ := m.Metadata()
				d := &Delivery{Msg: m, Subject: m.Subject(), MsgID: m.Headers().Get(jetstream.MsgIDHeader),
					RequestID: m.Headers().Get(HeaderRequestID), Data: m.Data()}
				if meta != nil {
					d.NumDelivered = meta.NumDelivered
				}
				if d.MsgID == "" && meta != nil {
					d.MsgID = fmt.Sprintf("%s-%d", spec.Stream, meta.Sequence.Stream)
				}
				hctx := WithRequestID(ctx, d.RequestID)
				err := handle(hctx, d)
				switch {
				case err == nil:
					_ = m.Ack()
				case errors.Is(err, ErrSkipAck):
					log.Warn("ack sengaja tidak dikirim (simulasi crash)", "request_id", d.RequestID,
						"subject", d.Subject, "msg_id", d.MsgID)
				default:
					log.Error("gagal memproses, Nak", "request_id", d.RequestID, "subject", d.Subject, "err", err)
					_ = m.NakWithDelay(time.Second)
				}
			}
		}
	}()
	return nil
}

// ProcessOnce menjalankan efek bisnis fn dan mencatat msg_id ke processed_messages dalam
// SATU transaksi (Algoritma consumer idempotent, Bab 5). dup=true bila msg_id sudah pernah
// diproses. Bila dedup=false (mode demo), pengecekan dilewati sehingga duplikat ikut diproses.
func ProcessOnce(ctx context.Context, pool *pgxpool.Pool, consumer, msgID string, dedup bool,
	fn func(tx pgx.Tx) error) (dup bool, err error) {
	err = InTx(ctx, pool, func(tx pgx.Tx) error {
		if dedup {
			tag, err := tx.Exec(ctx, `INSERT INTO processed_messages (consumer, msg_id) VALUES ($1,$2)
				ON CONFLICT DO NOTHING`, consumer, msgID)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				dup = true
				return nil // duplikat: tidak ada efek bisnis, commit kosong lalu ack
			}
		}
		return fn(tx)
	})
	return dup, err
}
