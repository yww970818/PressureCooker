module pressurecooker/manager

go 1.24.0

require github.com/go-sql-driver/mysql v1.9.3

require (
	filippo.io/edwards25519 v1.1.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/gorilla/websocket v1.5.3
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/sys v0.30.0 // indirect
)

require (
	github.com/redis/go-redis/v9 v9.22.0
	pressurecooker/execution v0.0.0
)

replace pressurecooker/execution => ../execution
