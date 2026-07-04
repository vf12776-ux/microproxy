#!/bin/bash

echo "Сборка для Linux..."
GOOS=linux GOARCH=amd64 go build -o microproxy-linux

echo "Сборка для Windows..."
GOOS=windows GOARCH=amd64 go build -o microproxy.exe

echo "Сборка для macOS..."
GOOS=darwin GOARCH=amd64 go build -o microproxy-macos

echo "Готово!"
ls -lh microproxy-*