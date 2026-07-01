#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <sys/socket.h>
#include <netinet/in.h>
#include <arpa/inet.h>
#include <netdb.h>
#include <sys/stat.h>
#include <pthread.h>

#define PORT 8080
#define CACHE_DIR "cache"
#define BUF_SIZE 65536

// Хэш-функция для имени файла кэша
unsigned long hash_url(const char *url) {
    unsigned long hash = 5381;
    int c;
    while ((c = *url++))
        hash = ((hash << 5) + hash) + c;
    return hash;
}

// Разбираем URL
int parse_url(const char *url, char *host, char *path) {
    const char *p = url;
    if (strncmp(p, "http://", 7) != 0) return -1;
    p += 7;
    const char *slash = strchr(p, '/');
    if (slash) {
        strncpy(host, p, slash - p);
        host[slash - p] = '\0';
        strcpy(path, slash);
    } else {
        strcpy(host, p);
        strcpy(path, "/");
    }
    return 0;
}

// Проверяем кэш
long check_cache(const char *url, char *buf) {
    char filename[512];
    snprintf(filename, sizeof(filename), "%s/%lu", CACHE_DIR, hash_url(url));
    
    FILE *f = fopen(filename, "rb");
    if (!f) return -1;
    
    long size = fread(buf, 1, BUF_SIZE - 1, f);
    fclose(f);
    if (size <= 0) return -1;
    buf[size] = '\0';
    printf("[HIT ] %s (из кэша)\n", url);
    return size;
}

// Сохраняем в кэш
void save_cache(const char *url, const char *buf, long size) {
    char filename[512];
    snprintf(filename, sizeof(filename), "%s/%lu", CACHE_DIR, hash_url(url));
    FILE *f = fopen(filename, "wb");
    if (f) {
        fwrite(buf, 1, size, f);
        fclose(f);
        printf("[SAVE] %s -> %s (%ld байт)\n", url, filename, size);
    }
}

// Идём в интернет
long fetch_from_origin(const char *host, const char *path, char *buf) {
    struct addrinfo hints = {0}, *res;
    hints.ai_family = AF_INET;
    hints.ai_socktype = SOCK_STREAM;
    
    if (getaddrinfo(host, "80", &hints, &res) != 0) return -1;
    
    int sock = socket(res->ai_family, res->ai_socktype, res->ai_protocol);
    if (sock < 0) { freeaddrinfo(res); return -1; }
    
    if (connect(sock, res->ai_addr, res->ai_addrlen) < 0) {
        close(sock); freeaddrinfo(res); return -1;
    }
    freeaddrinfo(res);
    
    char request[1024];
    snprintf(request, sizeof(request),
        "GET %s HTTP/1.0\r\n"
        "Host: %s\r\n"
        "User-Agent: MicroProxy/1.0\r\n"
        "Connection: close\r\n\r\n",
        path, host);
    
    send(sock, request, strlen(request), 0);
    
    long total = 0;
    int n;
    while ((n = recv(sock, buf + total, BUF_SIZE - total - 1, 0)) > 0) {
        total += n;
        if (total >= BUF_SIZE - 1) break;
    }
    buf[total] = '\0';
    close(sock);
    
    printf("[FETCH] %s%s (%ld байт)\n", host, path, total);
    return total;
}

// Поток для обработки одного клиента
void* handle_client(void* arg) {
    int client = *(int*)arg;
    free(arg);
    
    char buf[BUF_SIZE];
    int n = recv(client, buf, BUF_SIZE - 1, 0);
    if (n <= 0) { close(client); return NULL; }
    buf[n] = '\0';
    
    char method[16], url[512], version[16];
    if (sscanf(buf, "%15s %511s %15s", method, url, version) != 3 ||
        strcmp(method, "GET") != 0 ||
        strncmp(url, "http://", 7) != 0) {
        const char *err = "HTTP/1.0 400 Bad Request\r\n\r\n";
        send(client, err, strlen(err), 0);
        close(client);
        return NULL;
    }
    
    char host[256], path[512];
    if (parse_url(url, host, path) < 0) {
        close(client); return NULL;
    }
    
    char response[BUF_SIZE];
    long size;
    
    if ((size = check_cache(url, response)) > 0) {
        send(client, response, size, 0);
    } else {
        if ((size = fetch_from_origin(host, path, response)) > 0) {
            save_cache(url, response, size);
            send(client, response, size, 0);
        } else {
            const char *err = "HTTP/1.0 502 Bad Gateway\r\n\r\n";
            send(client, err, strlen(err), 0);
        }
    }
    
    close(client);
    return NULL;
}

int main() {
    mkdir(CACHE_DIR, 0755);
    
    int server_fd = socket(AF_INET, SOCK_STREAM, 0);
    int opt = 1;
    setsockopt(server_fd, SOL_SOCKET, SO_REUSEADDR, &opt, sizeof(opt));
    
    struct sockaddr_in addr = {0};
    addr.sin_family = AF_INET;
    addr.sin_addr.s_addr = INADDR_ANY;
    addr.sin_port = htons(PORT);
    
    if (bind(server_fd, (struct sockaddr*)&addr, sizeof(addr)) < 0) {
        perror("bind failed"); return 1;
    }
    if (listen(server_fd, 10) < 0) {
        perror("listen failed"); return 1;
    }
    
    printf("=== MicroProxy v2 (многопоточный) запущен на порту %d ===\n", PORT);
    printf("Настрой браузер: прокси 127.0.0.1:%d\n", PORT);
    
    while (1) {
        int *client = malloc(sizeof(int));
        *client = accept(server_fd, NULL, NULL);
        if (*client < 0) { free(client); continue; }
        
        pthread_t thread;
        pthread_create(&thread, NULL, handle_client, client);
        pthread_detach(thread);
    }
    
    close(server_fd);
    return 0;
}