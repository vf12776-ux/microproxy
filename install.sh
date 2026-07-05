#!/bin/bash

echo "Установка MicroProxy..."

# Копируем бинарник
sudo cp microproxy-linux /usr/local/bin/microproxy
sudo chmod +x /usr/local/bin/microproxy

# Копируем ярлык
sudo cp microproxy.desktop /usr/share/applications/

echo "✅ Установка завершена!"
echo "Найди MicroProxy в меню приложений → Сеть"