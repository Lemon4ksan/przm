CC ?= gcc
CFLAGS ?= -std=c11 -O3 -Wall -Wextra -Wno-unused-parameter -Iinclude
AR ?= ar

TARGET = test_przm_dec.exe
LIB = libprzm_dec.a

all: $(LIB) $(TARGET)

$(LIB): src/przm_dec.o
	$(AR) rcs $(LIB) src/przm_dec.o

src/przm_dec.o: src/przm_dec.c include/przm_dec.h
	$(CC) $(CFLAGS) -c src/przm_dec.c -o src/przm_dec.o

$(TARGET): tests/test_przm_dec.c $(LIB)
	$(CC) $(CFLAGS) tests/test_przm_dec.c -L. -l:libprzm_dec.a -o $(TARGET)

test: $(TARGET)
	./$(TARGET)

clean:
	rm -f $(TARGET) $(LIB) src/*.o

.PHONY: all test clean
