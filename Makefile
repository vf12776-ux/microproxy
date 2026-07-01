CC = gcc
CFLAGS = -Wall -O2
LDFLAGS = -lpthread

all: microproxy

microproxy: microproxy.c
	$(CC) $(CFLAGS) -o microproxy microproxy.c $(LDFLAGS)

clean:
	rm -f microproxy
	rm -rf cache

.PHONY: all clean