CC = gcc
CFLAGS = -std=c11 -O3 -Wall -Wextra -Wno-unused-parameter -Iinclude -Isrc
AR = ar

TARGET = test_przm_dec.exe
LIB = libprzm_dec.a

all: $(LIB) $(TARGET)

$(LIB): src/przm_dec.o src/przm_comp.o
	$(AR) rcs $(LIB) src/przm_dec.o src/przm_comp.o

src/przm_dec.o: src/przm_dec.c include/przm_dec.h
	$(CC) $(CFLAGS) -c src/przm_dec.c -o src/przm_dec.o

src/przm_comp.o: src/przm_comp.c include/przm_dec.h
	$(CC) $(CFLAGS) -c src/przm_comp.c -o src/przm_comp.o

$(TARGET): tests/test_przm_dec.c $(LIB)
	$(CC) $(CFLAGS) tests/test_przm_dec.c -L. -l:libprzm_dec.a -o $(TARGET)

test: $(TARGET)
	./$(TARGET)

clean:
	rm -f $(TARGET) $(LIB) src/*.o

.PHONY: all test clean
