# CLAUDE.md

อ่านไฟล์นี้ทุกครั้งก่อนเขียนโค้ด ถ้าคำสั่งที่ได้รับขัดกับไฟล์นี้ ให้ถามก่อน อย่าเดา

---

## 1. โปรเจกต์นี้คืออะไร

ระบบวิเคราะห์ BTC/USDT แบบ realtime สำหรับ **ผู้ใช้คนเดียว (owner)**
วิเคราะห์บน server 24/7 แล้วส่ง push notification เข้ามือถือเมื่อเจอสัญญาณ

**Scope ปัจจุบัน**
- เหรียญเดียว: BTCUSDT
- Timeframe หลัก: 1m, 5m (สไตล์ scalping) + 15m/1h ใช้เป็น trend filter
- Exchange: Binance (market data)

**ข้อจำกัดเด็ดขาด — ห้ามละเมิด**
- ระบบนี้ **ไม่ยิงออเดอร์** ไม่ว่ากรณีใด
- ห้ามเขียนโค้ดที่เรียก endpoint ประเภท order/trade/withdraw
- ห้ามใช้ API key ที่มีสิทธิ์ trade — market data ของ Binance เป็น public ไม่ต้องใช้ key
- ถ้าคิดว่าจำเป็นต้องยิงออเดอร์ ให้หยุดแล้วถาม ห้ามเขียนไว้ก่อน

Output ของระบบมีแค่: **สัญญาณ + เหตุผล + การแจ้งเตือน** เท่านั้น

---

## 2. Tech Stack

| ส่วน | เลือกใช้ |
|---|---|
| Backend | Go 1.22+ (monolith) |
| DB | PostgreSQL 16 + TimescaleDB extension |
| Migration | goose |
| DB access | pgx v5 + sqlc (ห้ามใช้ ORM) |
| Container | Docker + Docker Compose |
| Mobile | React Native (Expo) |
| Push | Firebase Cloud Messaging |
| Deploy | VPS 2 vCPU / 4 GB RAM / Ubuntu 24.04 |

**ห้ามเพิ่มโดยไม่ถาม:** Redis, Kafka, Kubernetes, gRPC, microservice, message queue, ORM

---

## 3. หลักการออกแบบ (สำคัญที่สุด)

### 3.1 Candle ที่ยังไม่ปิด ห้ามใช้คำนวณสัญญาณ
Binance ส่ง kline ที่ยังไม่ปิดมาด้วย (field `k.x == false`)
- Indicator และ signal ต้องคำนวณจาก **closed candle เท่านั้น**
- candle ที่ยังไม่ปิด เก็บแยกใน memory ได้ เพื่อแสดงผลบน UI แต่ห้ามป้อนเข้า strategy
- ละเมิดข้อนี้ = สัญญาณกระพริบ และ backtest ไม่ตรงกับของจริง

### 3.2 ห้ามมี look-ahead bias
Engine ต้องเห็นข้อมูลได้แค่ถึงเวลา `t` เท่านั้น ตอนตัดสินใจที่เวลา `t`
- Interface ของ strategy ต้องรับ "ข้อมูลถึงปัจจุบัน" ไม่ใช่ทั้ง slice
- โค้ดเดียวกันต้องรันได้ทั้ง live และ backtest (ต่างกันแค่แหล่งป้อนข้อมูล)
- ถ้า live กับ backtest ใช้ code path คนละอัน ถือว่าออกแบบผิด

### 3.3 Data integrity
- WebSocket หลุดเป็นเรื่องปกติ ต้องมี auto-reconnect + exponential backoff
- ทุกครั้งที่ reconnect ต้อง **backfill ผ่าน REST** เพื่ออุดช่วงที่ขาด
- ต้องมี gap detection: ถ้า candle ขาดช่วง ต้อง log + backfill ก่อนคำนวณต่อ
- ทุก candle เก็บด้วย `open_time` เป็น primary key (UTC, มิลลิวินาที) — idempotent upsert เสมอ

### 3.4 Fee และ slippage ต้องมีตั้งแต่วันแรก
scalping 1m-5m อ่อนไหวกับต้นทุนมาก ถ้า backtest ไม่หักต้นทุน ผลจะบวกเกินจริงจนไร้ความหมาย
- fee ต้องเป็น config ไม่ใช่ค่าคงที่ฝังในโค้ด
- default: taker 0.05%, slippage 1 tick
- ทุกรายงาน backtest ต้องแสดงผล **หลังหักต้นทุน** เท่านั้น

### 3.5 Spot vs Futures ยังไม่ตัดสินใจ
- ให้แยก market type เป็น config/enum ตั้งแต่แรก
- ห้าม hardcode endpoint หรือ symbol format ไว้ในโค้ดคำนวณ
- ยังไม่ต้อง implement futures logic (funding rate, leverage) แต่โครงสร้างต้องเปิดทางไว้

---

## 4. Coding Standards

- ตั้งชื่อไฟล์ snake_case, package สั้น ๆ ไม่มี underscore
- `error` ต้อง wrap ด้วย `fmt.Errorf("...: %w", err)` เสมอ ห้าม swallow
- ห้าม `panic()` ใน business logic
- ห้าม global mutable state ยกเว้น config ที่โหลดครั้งเดียวตอน start
- ทุก goroutine ต้องรับ `context.Context` และปิดตัวเองได้
- Money/price ใช้ `decimal.Decimal` (shopspring) **ห้ามใช้ float64 กับราคาและ balance**
  - float64 ใช้ได้เฉพาะกับค่า indicator ภายใน
- Log: `log/slog` แบบ structured เท่านั้น ห้าม `fmt.Println`
- Timestamp ทุกที่เป็น UTC ห้ามใช้ local time
- ทุก exported function มี doc comment

**Testing**
- Indicator ทุกตัวต้องมี unit test เทียบกับค่าที่รู้คำตอบ (fixture จากข้อมูลจริง)
- Test ห้ามเรียก network — ใช้ fixture ใน `testdata/`

---

## 5. Folder Structure

```
trading-platform/
├── CLAUDE.md
├── docs/
│   ├── decisions/          # ADR สั้น ๆ ไฟล์ละ 1 หน้า
│   └── prompts/            # phase-01.md, phase-02.md, ...
├── backend/
│   ├── cmd/
│   │   ├── api/            # REST + WS server
│   │   ├── collector/      # binance ingestion worker
│   │   └── backtest/       # CLI runner
│   ├── internal/
│   │   ├── config/
│   │   ├── domain/         # struct กลาง ไม่ import อะไรเลย
│   │   ├── market/         # binance ws + rest + backfill
│   │   ├── storage/        # postgres, sqlc generated
│   │   ├── indicator/
│   │   ├── trend/
│   │   ├── strategy/
│   │   ├── backtest/
│   │   └── notify/
│   ├── migrations/
│   └── testdata/
├── mobile/
└── deploy/
    ├── docker-compose.yml
    └── Caddyfile
```

**กฎการ import:** `domain` ห้าม import package อื่นในโปรเจกต์
ทิศทาง dependency: `cmd → internal/* → domain` เท่านั้น ห้ามย้อนกลับ

---

## 6. ลำดับการพัฒนา

Backtest มา **ก่อน** strategy โดยตั้งใจ — เพราะถ้าไม่มีเครื่องวัด จะเขียน strategy โดยไม่รู้ว่าใช้ได้ไหม

1. Skeleton + Docker + config + DB migration
2. Market data (WS + REST backfill + gap detection)
3. Indicator engine (EMA, RSI, ATR, VWAP)
4. **Backtest engine** ← ก่อน strategy
5. Trend filter (multi-timeframe)
6. Strategy + signal
7. Notification (FCM)
8. REST/WS API
9. React Native app

ทำทีละ phase ห้ามข้าม ห้ามเขียนล่วงหน้าไปยัง phase ถัดไป

---

## 7. Definition of Done (ทุก phase)

- [ ] `go build ./...` ผ่าน
- [ ] `go vet ./...` ผ่าน
- [ ] `go test ./...` ผ่าน
- [ ] `docker compose up` แล้วรันได้จริง
- [ ] ไม่มี TODO ค้างที่ไม่ได้เขียนอธิบายไว้
- [ ] อัปเดต `docs/decisions/` ถ้ามีการตัดสินใจใหม่

---

## 8. วิธีทำงานกับ Claude Code

- ทำทีละ phase ตาม `docs/prompts/phase-XX.md`
- ก่อนเขียนโค้ด ให้สรุปแผนสั้น ๆ ก่อน แล้วรอ approve
- ถ้าต้องเพิ่ม dependency ใหม่ ต้องบอกเหตุผลก่อน
- commit เล็ก ๆ message แบบ conventional commits (`feat:`, `fix:`, `chore:`)
- ถ้าเจอว่า spec ในไฟล์นี้ขัดแย้งกันเอง ให้หยุดแล้วถาม

---

## 9. เตือนความจริง

ระบบนี้คือ **เครื่องมือวัดผล** ไม่ใช่เครื่องพิมพ์เงิน
เป้าหมายของ 3 เดือนแรกคือได้ backtest ที่ซื่อสัตย์ ไม่ใช่ได้กำไร
ตัวชี้วัดที่ดู: max drawdown, Sharpe, win rate, profit factor — **ไม่ใช่กำไรรวมอย่างเดียว**
