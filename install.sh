#!/bin/bash

echo "Установка MicroProxy..."

# Копируем бинарник
sudo cp microproxy-linux /usr/local/bin/microproxy
sudo chmod +x /usr/local/bin/microproxy

# Копируем ярлык
sudo cp microproxy.desktop /usr/share/applications/

# Копируем иконку
sudo mkdir -p /usr/local/share/icons
sudo cp icon-256.png /usr/local/share/icons/microproxy.png

# Устанавливаем сертификат в системное хранилище
sudo cp ca.crt /usr/local/share/ca-certificates/microproxy.crt
sudo update-ca-certificates

echo "Устанавливаем сертификат во все браузеры..."

# Находим ВСЕ профили браузеров автоматически
find ~ -name "cert9.db" 2>/dev/null | while read db; do
    dir=$(dirname "$db")
    certutil -A -n "MicroProxy CA" -t "TC,," -i ca.crt -d sql:$dir 2>/dev/null
    echo "✅ Установлено в: $dir"
done

# Firefox snap — добавляем user.js
find ~/snap/firefox -name "cert9.db" 2>/dev/null | while read db; do
    dir=$(dirname "$db")
    echo 'user_pref("security.enterprise_roots.enabled", true);' > "$dir/user.js"
    echo "✅ Firefox настроен: $dir"
done

echo "✅ Установка завершена!"
echo "Перезапусти браузеры — HTTPS работает из коробки"