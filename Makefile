# campuscommunity 本地开发构建管理
# 依赖: make（Git Bash / WSL / choco install make）、docker、go、node
# 生成日期: 2026-08-06

BACKEND_DIR  := backend
FRONTEND_DIR := frontend

.PHONY: help deps dev dev-backend dev-frontend build tidy prod-preview clean

help:  ## 查看所有目标
	@echo "Available targets:"
	@echo "  deps          启动依赖中间件 (RabbitMQ)"
	@echo "  dev           同时启动前后端 (需 sh 风格 shell)"
	@echo "  dev-backend   仅启动后端"
	@echo "  dev-frontend  仅启动前端"
	@echo "  build         构建前端产物 + 后端二进制"
	@echo "  tidy          整理 Go 依赖"
	@echo "  prod-preview  拉起 Nginx 生产拓扑预览 (需先 make build)"
	@echo "  clean         清理构建产物与容器"

deps:  ## 启动依赖中间件 (RabbitMQ)
	docker compose up -d rabbitmq
	@echo "RabbitMQ -> AMQP :5672 | 管理 UI http://localhost:15672 (guest/guest)"

dev-backend:  ## 仅启动后端
	cd $(BACKEND_DIR) && go run cmd/main.go

dev-frontend:  ## 仅启动前端
	cd $(FRONTEND_DIR) && npm run dev

dev:  ## 同时启动前后端 (需 sh 风格 shell，否则用两个终端分别跑 dev-backend/dev-frontend)
	@echo "Starting backend and frontend in parallel (Ctrl+C 退出两者)..."
	@cd $(BACKEND_DIR) && go run cmd/main.go &
	@cd $(FRONTEND_DIR) && npm run dev

build:  ## 构建前端产物 + 后端二进制
	cd $(FRONTEND_DIR) && npm run build
	cd $(BACKEND_DIR) && go build -o bin/campuscommunity cmd/main.go

tidy:  ## 整理 Go 依赖
	cd $(BACKEND_DIR) && go mod tidy

prod-preview:  ## 拉起 Nginx 生产拓扑预览 (需先 make build)
	docker compose --profile prod-preview up -d

clean:  ## 清理构建产物与容器
	rm -rf $(BACKEND_DIR)/bin $(FRONTEND_DIR)/dist
	docker compose down
