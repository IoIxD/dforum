package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/diamondburned/arikawa/v3/discord"
	_ "github.com/lib/pq"
)

const postgresConfigSchema = `
CREATE TABLE IF NOT EXISTS "Config" (
	id SMALLINT PRIMARY KEY,
	version INTEGER NOT NULL,
	CHECK(id = 1)
);
`

const postgresSchema = `
CREATE TABLE "Message" (
	id BIGINT NOT NULL PRIMARY KEY,
	edited_at TIMESTAMP WITH TIME ZONE,
	author BIGINT NOT NULL,
	channel BIGINT NOT NULL,
	content TEXT NOT NULL,
	json TEXT NOT NULL
);

CREATE TABLE "Channel" (
	id BIGINT NOT NULL PRIMARY KEY,
	updated_at TIMESTAMP NOT NULL
);
`

var postgresMigrations = []string{""}

type Postgres struct {
	db          *sql.DB
	connectedAt time.Time
}

func (db *Postgres) Close() error {
	return db.db.Close()
}

func (db *Postgres) SetUpdatedAt(ctx context.Context, post discord.ChannelID, time time.Time) error {
	_, err := db.db.ExecContext(ctx, `UPDATE "Channel" SET updated_at = $1 WHERE id = $2`, time, post)
	return err
}

func (db *Postgres) UpdatedAt(ctx context.Context, post discord.ChannelID) (time.Time, error) {
	var t time.Time
	err := db.db.QueryRowContext(ctx, `SELECT updated_at FROM "Channel" WHERE id = $1`, post).Scan(&t)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, err
	}
	return t, nil
}

func (db *Postgres) UpdateMessages(ctx context.Context, post discord.ChannelID, msgs []discord.Message) error {
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM "Channel" WHERE id = $1)`, post).Scan(&exists)
	if err != nil {
		return fmt.Errorf("reading channel cache information: %w", err)
	}
	if exists {
		_, err = tx.ExecContext(ctx, `UPDATE "Channel" SET updated_at = $1 WHERE id = $2`, time.Now().UTC(), post)
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO "Channel" (id, updated_at) VALUES ($1, $2)`, post, time.Now().UTC())
	}
	if err != nil {
		return fmt.Errorf("writing channel cache information: %w", err)
	}
	insert, err := tx.PrepareContext(ctx, `INSERT INTO "Message" (id, author, channel, edited_at, content, json) VALUES($1, $2, $3, $4, $5, $6)`)
	if err != nil {
		return err
	}
	defer insert.Close()
	if !exists {
		for _, msg := range msgs {
			content := msg.Content
			msg.Content = ""
			jsonb, err := json.Marshal(msg)
			if err != nil {
				return err
			}
			_, err = insert.ExecContext(ctx, msg.ID, msg.Author.ID, msg.ChannelID, msg.EditedTimestamp.Time(), content, jsonb)
			if err != nil {
				return fmt.Errorf("inserting message: %w", err)
			}
		}
		return tx.Commit()
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, edited_at FROM "Message" WHERE channel = $1 ORDER BY id ASC`, post)
	if err != nil {
		return err
	}
	defer rows.Close()
	var toDelete []discord.MessageID
	var toInsert []discord.Message
	var toUpdate []discord.Message
	for _, msg := range msgs {
		var id discord.MessageID
		var updated time.Time
		exists := false
		for rows.Next() {
			if err = rows.Scan(&id, &updated); err != nil {
				return err
			}
			if id != msg.ID {
				toDelete = append(toDelete, id)
				continue
			}
			exists = true
		}
		if !exists {
			toInsert = append(toInsert, msg)
			continue
		}
		if updated.Before(msg.EditedTimestamp.Time()) {
			toUpdate = append(toUpdate, msg)
		}
	}
	for rows.Next() {
		var id discord.MessageID
		var updated time.Time
		if err = rows.Scan(&id, &updated); err != nil {
			return err
		}
		toDelete = append(toDelete, id)
	}
	if len(toDelete) > 0 {
		del, err := tx.PrepareContext(ctx, `DELETE FROM "Message" WHERE ID = $1`)
		if err != nil {
			return err
		}
		defer del.Close()
		for _, id := range toDelete {
			if _, err := del.ExecContext(ctx, id); err != nil {
				return err
			}
		}
	}
	if len(toUpdate) > 0 {
		update, err := tx.PrepareContext(ctx, `UPDATE "Message" SET content = $1, edited_at = $2, json = $3 WHERE id = $4`)
		if err != nil {
			return err
		}
		defer update.Close()
		for _, msg := range toUpdate {
			content := msg.Content
			msg.Content = ""
			jsonb, err := json.Marshal(msg)
			if err != nil {
				return fmt.Errorf("marshaling message as JSON: %v", err)
			}
			if _, err := update.ExecContext(ctx, content, msg.EditedTimestamp.Time(), jsonb, msg.ID); err != nil {
				return err
			}
		}
	}
	if len(toInsert) > 0 {
		for _, msg := range toInsert {
			content := msg.Content
			msg.Content = ""
			jsonb, err := json.Marshal(msg)
			if err != nil {
				return err
			}
			_, err = insert.ExecContext(ctx, msg.ID, msg.Author.ID, msg.ChannelID, msg.EditedTimestamp.Time(), content, jsonb)
			if err != nil {
				return fmt.Errorf("inserting message: %w", err)
			}
		}
	}
	return tx.Commit()
}

func (db *Postgres) InsertMessage(ctx context.Context, msg discord.Message) error {
	content := msg.Content
	msg.Content = ""
	jsonb, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshaling message as JSON: %v", err)
	}
	_, err = db.db.ExecContext(ctx, `INSERT INTO "Message" (id, author, channel, edited_at, content, json) VALUES($1, $2, $3, $4, $5, $6) ON CONFLICT DO NOTHING`,
		msg.ID, msg.Author.ID, msg.ChannelID, msg.EditedTimestamp.Time(), content, jsonb)
	return err
}

func (db *Postgres) DeleteMessage(ctx context.Context, msg discord.MessageID) error {
	_, err := db.db.ExecContext(ctx, `DELETE FROM "Message" WHERE id = $1`, msg)
	return err
}

func (db *Postgres) UpdateMessage(ctx context.Context, msg discord.Message) error {
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var edited time.Time
	err = tx.QueryRowContext(ctx, `SELECT edited_at FROM "Message" WHERE id = $1`, msg.ID).Scan(&edited)
	if err != nil && errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if msg.EditedTimestamp.Time().Before(edited) {
		return nil
	}
	content := msg.Content
	msg.Content = ""
	jsonb, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshaling message as JSON: %v", err)
	}
	_, err = db.db.ExecContext(ctx, `UPDATE "Message" SET content = $1, edited_at = $2, json = $3 WHERE id = $4`,
		content, msg.EditedTimestamp.Time(), jsonb, msg.ID)
	return err
}

func (db *Postgres) MessagesAfter(ctx context.Context, ch discord.ChannelID, msg discord.MessageID, limit uint) (msgs []discord.Message, hasbefore bool, err error) {
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM "Message" WHERE channel = $1 AND id <= $2)`, ch, msg).Scan(&hasbefore)
	if err != nil {
		return
	}
	rows, err := db.db.QueryContext(ctx, `SELECT content, json FROM "Message" WHERE channel = $1 AND id > $2 ORDER BY id ASC LIMIT $3`,
		ch, msg, limit)
	if err != nil {
		err = fmt.Errorf("querying messages: %v", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var content string
		var jsonb []byte
		if err = rows.Scan(&content, &jsonb); err != nil {
			err = fmt.Errorf("error scanning message: %w", err)
			return
		}
		var msg discord.Message
		if err = json.Unmarshal(jsonb, &msg); err != nil {
			err = fmt.Errorf("unmrshaling message content: %w", err)
			return
		}
		msg.Content = content
		msgs = append(msgs, msg)
	}
	return
}

func (db *Postgres) MessagesBefore(ctx context.Context, ch discord.ChannelID, msg discord.MessageID, limit uint) (msgs []discord.Message, hasafter bool, err error) {
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM "Message" WHERE channel = $1 AND id >= $2)`, ch, msg).Scan(&hasafter)
	if err != nil {
		return
	}
	rows, err := tx.QueryContext(ctx, `SELECT content, json FROM (SELECT id, content, json FROM "Message" WHERE channel = $1 AND id < $2 ORDER BY id DESC LIMIT $3) AS x ORDER BY id ASC`,
		ch, msg, limit)
	if err != nil {
		err = fmt.Errorf("querying messages: %v", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var content string
		var jsonb []byte
		if err = rows.Scan(&content, &jsonb); err != nil {
			err = fmt.Errorf("error scanning message content: %w", err)
			return
		}
		var msg discord.Message
		if err = json.Unmarshal(jsonb, &msg); err != nil {
			err = fmt.Errorf("unmrshaling message content: %w", err)
			return
		}
		msg.Content = content
		msgs = append(msgs, msg)
	}
	return
}

func OpenPostgres(source string) (Database, error) {
	sqldb, err := sql.Open("postgres", source)
	if err != nil {
		return nil, err
	}
	sqldb.SetMaxOpenConns(25)

	db := &Postgres{db: sqldb, connectedAt: time.Now()}
	if err := db.upgrade(); err != nil {
		sqldb.Close()
		return nil, err
	}
	return db, nil
}

func (db *Postgres) upgrade() error {
	tx, err := db.db.Begin()
	if err != nil {
		return fmt.Errorf("couldn't start db transaction: %w", err)
	}
	defer tx.Rollback()
	_, err = tx.Exec(postgresConfigSchema)
	if err != nil {
		return err
	}
	var version int
	err = tx.QueryRow(`SELECT version FROM "Config"`).Scan(&version)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("couldn't query schema version: %v", err)
	}
	if version > len(postgresMigrations) {
		log.Fatalln("database is from a newer dforum")
	}
	if version == 0 {
		if _, err := tx.Exec(postgresSchema); err != nil {
			return fmt.Errorf("failed while executing schema: %v", err)
		}
	} else if version < len(postgresMigrations) {
		for version < len(postgresMigrations) {
			_, err := tx.Exec(postgresMigrations[version])
			if err != nil {
				return fmt.Errorf("failed while executing migration %d: %v", version, err)
			}
			version++
		}
	}
	_, err = tx.Exec(`INSERT INTO "Config" (id, version) VALUES (1, $1)
	ON CONFLICT (id) DO UPDATE SET version = $1`, len(postgresMigrations))
	if err != nil {
		return fmt.Errorf("failed to change schema version: %v", err)
	}
	return tx.Commit()
}
