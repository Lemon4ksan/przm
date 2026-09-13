FC = gfortran
FFLAGS = -O2 -Wall

all: test_przm_dec.exe
	./test_przm_dec.exe

test_przm_dec.exe: przm_types.f90 przm_dict.f90 przm_math.f90 przm_vans.f90 przm_pws.f90 przm_lz.f90 przm_dec.f90 test_przm_dec.f90
	$(FC) $(FFLAGS) -o $@ $^

clean:
	rm -f *.mod *.exe *.o
