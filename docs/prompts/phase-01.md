# Phase 01 — Skeleton, Config, Docker, Database

> **อัปเดต 2026-08-03:** phase นี้เสร็จและ merge เข้า main แล้ว จากนั้นโครงสร้างถูกย้ายไป
> clean architecture และเปลี่ยน `backend/` เป็น `server/` — path ในเอกสารนี้อัปเดตตามแล้ว
> โครงสร้างจริงดูที่ `CLAUDE.md` section 5 และเหตุผลที่ `docs/decisions/0005-clean-architecture-layout.md`

> อ่าน `CLAUDE.md` ให้จบก่อนเริ่ม
> Phase นี้ **ยังไม่ต้องต่อ Binance และยังไม่ต้องคำนวณ indicator**
> เป้าหมายคือได้โครงที่รันได้จริงและมี schema ที่ถูกต้อง

---

## เป้าหมาย

จบ phase นี้แล้วต้อง `docker compose up` ขึ้นได้ เห็น API ตอบ `/health` และมีตาราง DB ครบ

---

## 1. โครงสร้างโปรเจกต์

สร้างตาม section 5 ของ `CLAUDE.md`
- `go mod init` module name: `github.com/spioneracorei8/btcusd-trading-platform/server`
- entry point: `main.go` (API), `collector/main.go`, `backtest/main.go` โดย collector และ backtest ยังเป็น stub ที่ log แล้วออก
- `.gitignore`, `.env.example`, `Makefile` (target: `build`, `test`, `lint`, `migrate-up`, `migrate-down`, `sqlc`)

## 2. Config

`config/env.go`
- โหลดจาก environment variable ล้วน (12-factor) ไม่มีไฟล์ config
- struct แยกกลุ่ม: `App`, `Database`, `Market`, `Notify`
- ต้อง validate ตอน start ถ้าค่าจำเป็นหาย ให้ fail ทันทีพร้อมบอกชื่อ env ที่ขาด
- ค่าที่ต้องมี:
  - `APP_ENV` (dev/prod), `LOG_LEVEL`, `HTTP_PORT`
  - `DATABASE_URL`
  - `MARKET_SYMBOL` (default `BTCUSDT`)
  - `MARKET_TYPE` (enum: `spot` | `futures`, default `spot`) — ยังไม่ implement futures แต่ต้องมี field
  - `MARKET_TIMEFRAMES` (comma separated, default `1m,5m,15m,1h`)
  - `FEE_TAKER_PCT` (default `0.05`), `SLIPPAGE_TICKS` (default `1`)

## 3. Logging

- `log/slog` JSON handler ตอน prod, text handler ตอน dev
- middleware log ทุก HTTP request: method, path, status, duration, request_id

## 4. Database Schema (goose migration)

ใช้ PostgreSQL + TimescaleDB

### `candles`
| column | type | note |
|---|---|---|
| symbol | text | |
| market_type | text | spot/futures |
| timeframe | text | 1m, 5m, ... |
| open_time | timestamptz | UTC, เวลาเปิดแท่ง |
| close_time | timestamptz | |
| open, high, low, close | numeric(20,8) | ห้ามใช้ double |
| volume | numeric(30,8) | |
| quote_volume | numeric(30,8) | |
| trade_count | integer | |
| is_closed | boolean | เก็บเฉพาะ true เท่านั้นในตารางนี้ |
| created_at | timestamptz | default now() |

- PRIMARY KEY `(symbol, market_type, timeframe, open_time)` → upsert ได้แบบ idempotent
- แปลงเป็น hypertable ด้วย `create_hypertable` partition ตาม `open_time` (chunk 7 วัน)
- index เพิ่ม: `(symbol, timeframe, open_time DESC)`

### `data_gaps`
บันทึกช่วงข้อมูลที่ตรวจพบว่าขาด เพื่อให้ backfill ตามเก็บ และเพื่อให้ backtest รู้ว่าช่วงไหนเชื่อไม่ได้
- id, symbol, market_type, timeframe, gap_start, gap_end, detected_at, filled_at (nullable), note

### `signals`
- id (uuid), symbol, market_type, timeframe
- signal_time timestamptz — เวลาปิดของแท่งที่ทำให้เกิดสัญญาณ
- direction text (`long` | `short` | `flat`)
- strength numeric(5,2)
- entry_price, stop_loss, take_profit numeric(20,8)
- strategy_name text, strategy_version text
- reason jsonb — เก็บค่า indicator ที่ทำให้ตัดสินใจ
- created_at
- UNIQUE `(strategy_name, strategy_version, symbol, timeframe, signal_time)` → กันแจ้งซ้ำ

### `notifications`
- id, signal_id (fk), channel text, status text (`pending`|`sent`|`failed`)
- attempts int, last_error text, sent_at, created_at

**ยังไม่ต้องสร้าง:** ตาราง users, trades, orders, positions — ระบบนี้ผู้ใช้คนเดียวและไม่ยิงออเดอร์

## 5. Storage layer

`database/` (pool, convert, sqlc) + `services/<domain>/repository/`

- ใช้ pgx v5 pool + sqlc
- เขียน query เริ่มต้น: `UpsertCandle`, `GetCandles(symbol, timeframe, from, to)`, `GetLatestCandle`, `InsertSignal`, `InsertGap`
- `UpsertCandle` ต้องเป็น `ON CONFLICT ... DO UPDATE`

## 6. HTTP server

`main.go` + `server/server.go` + `routes/api.go` + `services/health/` — ใช้ `net/http` + `chi` (chi อนุญาตให้ใช้ได้)
- `GET /health` → 200 `{"status":"ok"}`
- `GET /ready` → เช็ค DB ping ด้วย ถ้าไม่ผ่านคืน 503
- graceful shutdown บน SIGTERM/SIGINT timeout 10 วินาที

## 7. Docker

`deploy/docker-compose.yml`
- service: `postgres` (image `timescale/timescaledb:latest-pg16`), `api`, `collector`
- healthcheck ของ postgres และให้ api `depends_on: condition: service_healthy`
- named volume สำหรับ pgdata
- multi-stage Dockerfile, final image เป็น distroless หรือ alpine, binary ต้อง static
- อย่า mount source code ใน prod compose

## 8. Test

- unit test ของ config validation (กรณี env ขาด, กรณี enum ผิด)
- integration test ของ `UpsertCandle` ว่าเรียกซ้ำแล้วไม่เกิดแถวซ้ำ (ใช้ testcontainers หรือ skip ถ้าไม่มี docker)

---

## Definition of Done

- [ ] `docker compose up` แล้ว `curl localhost:8080/health` ได้ 200
- [ ] `make migrate-up` สร้างตารางครบและ `candles` เป็น hypertable จริง (verify ด้วย query)
- [ ] `go build ./... && go vet ./... && go test ./...` ผ่านทั้งหมด
- [ ] ยิง `UpsertCandle` ด้วยข้อมูลเดิมซ้ำ 2 ครั้ง แล้วมี 1 แถว
- [ ] ไม่มีโค้ดที่แตะ endpoint order/trade ของ exchange
- [ ] `.env.example` ครบทุกตัวแปร

---

## สิ่งที่ห้ามทำใน phase นี้

- ห้ามต่อ Binance WebSocket
- ห้ามเขียน indicator
- ห้ามเขียน strategy
- ห้ามเพิ่ม Redis / message queue
- ห้ามสร้างตาราง orders/trades

---

## เริ่มยังไง

สรุปแผนการทำงานเป็นข้อ ๆ ก่อน แล้วรอ approve จากนั้นค่อยลงมือ commit ทีละส่วน
