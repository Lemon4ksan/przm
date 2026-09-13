program bench_przm_f_symp
  use iso_c_binding
  use przm_dec
  implicit none

  integer(c_int8_t) :: frame(28), dst(128)
  integer(c_int64_t) :: res
  integer :: iter
  real :: start_time, end_time
  integer, parameter :: ITERS = 10000000

  frame(1) = 80 ! 'P'
  frame(2) = 77 ! 'M'
  frame(3) = 89 ! 'Y'
  frame(4) = 83 ! 'S'
  frame(5) = 1  ! Shear mode
  frame(6) = 5; frame(7) = 10; frame(8) = 31
  frame(9) = 16; frame(10) = 0; frame(11) = 0; frame(12) = 0
  frame(13:28) = [int(z'12', c_int8_t), int(z'34', c_int8_t), int(z'56', c_int8_t), int(z'78', c_int8_t), &
                  int(z'AB', c_int8_t), int(z'CD', c_int8_t), int(z'EF', c_int8_t), int(z'01', c_int8_t), &
                  int(z'42', c_int8_t), int(z'69', c_int8_t), int(z'99', c_int8_t), int(z'AA', c_int8_t), &
                  int(z'FF', c_int8_t), int(z'00', c_int8_t), int(z'55', c_int8_t), int(z'AA', c_int8_t)]

  call cpu_time(start_time)
  do iter = 1, ITERS
      res = przm_decompress(frame, 28_c_size_t, dst, 128_c_size_t)
  end do
  call cpu_time(end_time)

  print *, "Fortran Symplectic Transform (10M iters): ", end_time - start_time, " seconds"

end program bench_przm_f_symp
