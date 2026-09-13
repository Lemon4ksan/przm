program bench_przm_f
  use iso_c_binding
  use przm_dec
  implicit none

  integer(c_int8_t) :: orig(16), fwd(16), frame(28), dst(128)
  integer(c_int64_t) :: res
  integer :: i, s, iter
  real :: start_time, end_time
  integer, parameter :: ITERS = 10000000

  do i = 1, 16
      orig(i) = int(i - 1, c_int8_t)
  end do
  fwd = orig
  do s = 1, 15
      do i = 16, s + 1, -1
          fwd(i) = fwd(i) - fwd(i-1)
      end do
  end do

  frame(1) = 76 ! 'L'
  frame(2) = 65 ! 'A'
  frame(3) = 72 ! 'H'
  frame(4) = 77 ! 'M'
  frame(5) = 1
  frame(6) = 0; frame(7) = 0; frame(8) = 0
  frame(9) = 16; frame(10) = 0; frame(11) = 0; frame(12) = 0
  frame(13:28) = fwd(1:16)

  call cpu_time(start_time)
  do iter = 1, ITERS
      res = przm_decompress(frame, 28_c_size_t, dst, 128_c_size_t)
  end do
  call cpu_time(end_time)

  print *, "Fortran Mahler Transform (10M iters): ", end_time - start_time, " seconds"

end program bench_przm_f
