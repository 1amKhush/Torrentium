package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	_ "github.com/mattn/go-sqlite3"
)

type LocalFile struct {
	ID        string
	CID       string
	Filename  string
	FileSize  int64
	FilePath  string
	FileHash  string
	CreatedAt time.Time
}

type Download struct {
	ID           string
	CID          string
	Filename     string
	FileSize     int64
	DownloadPath string
	DownloadedAt time.Time
	Status       string
}

// Piece tracks chunked pieces for resume and verification
type Piece struct {
	ID        string
	CID       string
	Index     int64
	Offset    int64
	Size      int64
	Hash      string
	Have      bool
	UpdatedAt time.Time
}

// PeerScore stores reputation
type PeerScore struct {
	PeerID string
	Score  float64
	SeenAt time.Time
}

type Repository struct {
	DB *sql.DB
}

func NewRepository(db *sql.DB) *Repository { return &Repository{DB: db} }

var DB *sql.DB

func InitDB() *sql.DB {
	if err := godotenv.Load(); err != nil {
		//log.Printf("Warning: Could not load .env file: %v", err)
	}
	dbpath := os.Getenv("SQLITE_DB_PATH")
	if dbpath == "" {
		dbpath = "./peer.db"
	}
	var err error
	DB, err = sql.Open("sqlite3", dbpath)
	if err != nil {
		log.Fatalf("Error creating DB connection: %v", err)
	}
	if err = DB.Ping(); err != nil {
		log.Fatalf("Error connecting to DB: %v", err)
	}
	if err := createTables(DB); err != nil {
		log.Fatalf("Error creating tables: %v", err)
	}
	log.Println("Successfully connected to peer database")
	return DB
}

func createTables(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS local_files (
			id TEXT PRIMARY KEY,
			cid TEXT UNIQUE NOT NULL,
			filename TEXT NOT NULL,
			file_size INTEGER NOT NULL,
			file_path TEXT NOT NULL,
			file_hash TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS downloads (
			id TEXT PRIMARY KEY,
			cid TEXT UNIQUE NOT NULL,
			filename TEXT NOT NULL,
			file_size INTEGER NOT NULL,
			download_path TEXT NOT NULL,
			downloaded_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			status TEXT DEFAULT 'completed'
		);`,
		`CREATE TABLE IF NOT EXISTS pieces (
			id TEXT PRIMARY KEY,
			cid TEXT NOT NULL,
			idx INTEGER NOT NULL,
			offset INTEGER NOT NULL,
			size INTEGER NOT NULL,
			hash TEXT NOT NULL,
			have INTEGER NOT NULL DEFAULT 0,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE (cid, idx)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_pieces_cid ON pieces(cid);`,
		`CREATE TABLE IF NOT EXISTS peer_scores (
			peer_id TEXT PRIMARY KEY,
			score REAL NOT NULL,
			seen_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS metadata_index (
			cid TEXT PRIMARY KEY,
			filename TEXT NOT NULL,
			file_size INTEGER NOT NULL,
			file_hash TEXT NOT NULL
		);`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("schema error: %w", err)
		}
	}
	return nil
}

func (r *Repository) AddLocalFile(ctx context.Context, cid, filename string, fileSize int64, filePath, fileHash string) error {
	q := `INSERT INTO local_files (id, cid, filename, file_size, file_path, file_hash, created_at)
	      VALUES (?, ?, ?, ?, ?, ?, ?)
	      ON CONFLICT(cid) DO UPDATE SET filename=excluded.filename, file_path=excluded.file_path, file_size=excluded.file_size, file_hash=excluded.file_hash`
	_, err := r.DB.ExecContext(ctx, q, uuid.New().String(), cid, filename, fileSize, filePath, fileHash, time.Now())
	if err != nil {
		return err
	}
	// update metadata index for search
	_, _ = r.DB.ExecContext(ctx, `INSERT INTO metadata_index (cid, filename, file_size, file_hash)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(cid) DO UPDATE SET filename=excluded.filename, file_size=excluded.file_size, file_hash=excluded.file_hash`,
		cid, filename, fileSize, fileHash)
	return nil
}

func (r *Repository) GetLocalFiles(ctx context.Context) ([]LocalFile, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT id, cid, filename, file_size, file_path, file_hash, created_at FROM local_files ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var files []LocalFile
	for rows.Next() {
		var f LocalFile
		if err := rows.Scan(&f.ID, &f.CID, &f.Filename, &f.FileSize, &f.FilePath, &f.FileHash, &f.CreatedAt); err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, rows.Err()
}

func (r *Repository) GetLocalFileByCID(ctx context.Context, cid string) (*LocalFile, error) {
	var f LocalFile
	err := r.DB.QueryRowContext(ctx, `SELECT id, cid, filename, file_size, file_path, file_hash, created_at FROM local_files WHERE cid = ?`, cid).
		Scan(&f.ID, &f.CID, &f.Filename, &f.FileSize, &f.FilePath, &f.FileHash, &f.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

func (r *Repository) DeleteLocalFile(ctx context.Context, cid string) error {
	_, err := r.DB.ExecContext(ctx, `DELETE FROM local_files WHERE cid=?`, cid)
	return err
}

func (r *Repository) AddDownload(ctx context.Context, cid, filename string, fileSize int64, downloadPath string) error {
	_, err := r.DB.ExecContext(ctx, `INSERT INTO downloads (id, cid, filename, file_size, download_path, downloaded_at, status)
		VALUES (?, ?, ?, ?, ?, ?, 'completed') ON CONFLICT(cid) DO UPDATE SET status='completed', downloaded_at=excluded.downloaded_at, download_path=excluded.download_path`,
		uuid.New().String(), cid, filename, fileSize, downloadPath, time.Now())
	return err
}

func (r *Repository) UpsertPiece(ctx context.Context, cid string, idx int64, offset, size int64, hash string, have bool) error {
	_, err := r.DB.ExecContext(ctx, `INSERT INTO pieces (id, cid, idx, offset, size, hash, have, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(cid, idx) DO UPDATE SET have=excluded.have, updated_at=excluded.updated_at`,
		uuid.New().String(), cid, idx, offset, size, hash, boolToInt(have), time.Now())
	return err
}

func (r *Repository) GetPieces(ctx context.Context, cid string) ([]Piece, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT id, cid, idx, offset, size, hash, have, updated_at FROM pieces WHERE cid=? ORDER BY idx ASC`, cid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Piece
	for rows.Next() {
		var p Piece
		var haveInt int
		if err := rows.Scan(&p.ID, &p.CID, &p.Index, &p.Offset, &p.Size, &p.Hash, &haveInt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		p.Have = haveInt == 1
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *Repository) MissingPieces(ctx context.Context, cid string) ([]Piece, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT id, cid, idx, offset, size, hash, have, updated_at FROM pieces WHERE cid=? AND have=0 ORDER BY idx ASC`, cid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Piece
	for rows.Next() {
		var p Piece
		var haveInt int
		if err := rows.Scan(&p.ID, &p.CID, &p.Index, &p.Offset, &p.Size, &p.Hash, &haveInt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		p.Have = false
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *Repository) SetPeerScore(ctx context.Context, peerID string, delta float64) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var score float64
	err = tx.QueryRowContext(ctx, `SELECT score FROM peer_scores WHERE peer_id=?`, peerID).Scan(&score)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			_, err = tx.ExecContext(ctx, `INSERT INTO peer_scores (peer_id, score, seen_at) VALUES (?, ?, ?)`, peerID, 10.0+delta, time.Now())
		} else {
			return err
		}
	} else {
		score += delta
		if score < -50 {
			score = -50
		}
		if score > 100 {
			score = 100
		}
		_, err = tx.ExecContext(ctx, `UPDATE peer_scores SET score=?, seen_at=? WHERE peer_id=?`, score, time.Now(), peerID)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Repository) GetPeerScore(ctx context.Context, peerID string) (float64, error) {
	var s float64
	err := r.DB.QueryRowContext(ctx, `SELECT score FROM peer_scores WHERE peer_id=?`, peerID).Scan(&s)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	return s, nil
}

func (r *Repository) SearchByFilename(ctx context.Context, q string) ([]LocalFile, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT cid, filename, file_size, '' as file_path, '' as file_hash, CURRENT_TIMESTAMP FROM metadata_index WHERE filename LIKE ? ORDER BY filename`, "%"+q+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var res []LocalFile
	for rows.Next() {
		var lf LocalFile
		if err := rows.Scan(&lf.CID, &lf.Filename, &lf.FileSize, &lf.FilePath, &lf.FileHash, &lf.CreatedAt); err != nil {
			return nil, err
		}
		res = append(res, lf)
	}
	return res, rows.Err()
}

func boolToInt(b bool) int { if b { return 1 }; return 0 }
