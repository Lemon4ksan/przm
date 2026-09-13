module przm_math
  use iso_c_binding
  use przm_types
  implicit none

contains

  function gf256_mul(a_in, b_in) result(p)
    integer(c_int8_t), intent(in) :: a_in, b_in
    integer(c_int8_t) :: p, a, b, hi
    integer :: i
    a = a_in
    b = b_in
    p = 0
    do i = 1, 8
      if (iand(b, 1_c_int8_t) /= 0) p = ieor(p, a)
      hi = iand(a, int(128, c_int8_t)) ! 0x80
      a = ishft(a, 1)
      if (hi /= 0) a = ieor(a, int(27, c_int8_t)) ! 0x1B
      b = iand(ishft(b, -1), 127_c_int8_t)
    end do
  end function gf256_mul

  subroutine symplectic_shear_inplace(buf, n, s00, s01, s11)
    integer(c_int8_t), intent(inout) :: buf(*)
    integer(c_size_t), intent(in) :: n
    integer(c_int8_t), intent(in) :: s00, s01, s11
    integer(c_size_t) :: i, limit
    integer(c_int8_t) :: q0, q1, p0, p1
    
    if (s00 == 0 .and. s01 == 0 .and. s11 == 0) return
    limit = (n / 4) * 4
    do i = 1, limit, 4
      q0 = buf(i); q1 = buf(i+1); p0 = buf(i+2); p1 = buf(i+3)
      buf(i)   = ieor(q0, ieor(gf256_mul(s00, p0), gf256_mul(s01, p1)))
      buf(i+1) = ieor(q1, ieor(gf256_mul(s01, p0), gf256_mul(s11, p1)))
    end do
  end subroutine symplectic_shear_inplace

  subroutine mahler16_inverse(buf)
    integer(c_int8_t), intent(inout) :: buf(16)
    integer :: s, i
    do s = 15, 1, -1
      do i = s + 1, 16
        buf(i) = buf(i) + buf(i-1)
      end do
    end do
  end subroutine mahler16_inverse

end module przm_math
