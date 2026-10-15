CC = gcc
CFLAGS = -std=c11 -O3 -Wall -Wextra -Wno-unused-parameter -Iinclude -Isrc -Itests
AR = ar

TARGET = test_przm_dec.exe
LIB = libprzm_dec.a

all: $(LIB) $(TARGET)

$(LIB): src/przm_dec.o
	$(AR) rcs $(LIB) src/przm_dec.o

src/przm_dec.o: src/przm_dec.c include/przm_dec.h
	$(CC) $(CFLAGS) -c src/przm_dec.c -o src/przm_dec.o

tests/przm_comp.o: tests/przm_comp.c tests/przm_comp.h
	$(CC) $(CFLAGS) -c tests/przm_comp.c -o tests/przm_comp.o

$(TARGET): tests/test_przm_dec.c tests/przm_comp.o $(LIB)
	$(CC) $(CFLAGS) tests/test_przm_dec.c tests/przm_comp.o -L. -l:libprzm_dec.a -o $(TARGET)

test: $(TARGET)
	./$(TARGET)

clean:
	rm -f $(TARGET) $(LIB) src/*.o tests/*.o

format:
	go fmt ./...
	addlicense -c "Lemon4ksan" -l bsd -ignore "**/*.yml" .
	golangci-lint run --fix --allow-parallel-runners ./...

.PHONY: all test clean
