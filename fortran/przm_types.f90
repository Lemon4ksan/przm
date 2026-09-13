module przm_types
  use iso_c_binding
  implicit none

  ! Error Codes
  integer(c_int64_t), parameter :: PRZM_OK = 0
  integer(c_int64_t), parameter :: PRZM_ERR_CORRUPTED_FRAME = -1
  integer(c_int64_t), parameter :: PRZM_ERR_INVALID_MAGIC = -2
  integer(c_int64_t), parameter :: PRZM_ERR_BUFFER_TOO_SMALL = -3
  integer(c_int64_t), parameter :: PRZM_ERR_EXCEEDS_MAX_BLOCK = -4
  integer(c_int64_t), parameter :: PRZM_ERR_OFFSET_OOB = -5
  integer(c_int64_t), parameter :: PRZM_ERR_UNSUPPORTED_MODE = -6

  ! Magic Numbers
  integer(c_int32_t), parameter :: PRZM_MAGIC_STANDARD = int(z'4D5A5250', c_int32_t)
  integer(c_int32_t), parameter :: PRZM_MAGIC_SYMPLECTIC = int(z'53594D50', c_int32_t)
  integer(c_int32_t), parameter :: PRZM_MAGIC_MAHLER = int(z'4D41484C', c_int32_t)
  integer(c_int32_t), parameter :: PRZM_MAGIC_ARM64 = int(z'41524D36', c_int32_t)
  integer(c_int32_t), parameter :: PRZM_MAGIC_SHEAF = int(z'53484541', c_int32_t)

  integer(c_int8_t), parameter :: PRZM_MAGIC_PWS_0 = 80 ! 'P'
  integer(c_int8_t), parameter :: PRZM_MAGIC_PWS_1 = 87 ! 'W'
  integer(c_int8_t), parameter :: PRZM_MAGIC_VM_0 = 86  ! 'V'
  integer(c_int8_t), parameter :: PRZM_MAGIC_VM_1 = 77  ! 'M'
  integer(c_int8_t), parameter :: PRZM_MAGIC_GGTC_0 = 71 ! 'G'
  integer(c_int8_t), parameter :: PRZM_MAGIC_GGTC_1 = 71 ! 'G'
  integer(c_int8_t), parameter :: PRZM_MAGIC_VANS_0 = 86 ! 'V'
  integer(c_int8_t), parameter :: PRZM_MAGIC_VANS_1 = 65 ! 'A'

  integer(c_int8_t), parameter :: PRZM_VERSION_1 = 1

  integer(c_size_t), parameter :: PRZM_HEADER_SIZE = 8
  integer(c_size_t), parameter :: PRZM_MAX_BLOCK_SIZE = 16777216
  integer(c_size_t), parameter :: PRZM_SAFETY_PADDING = 32
  
  ! Flags
  integer(c_int8_t), parameter :: PRZM_FLAG_UNCOMPRESSED = 1
  integer(c_int8_t), parameter :: PRZM_FLAG_DICT_REF = 2
  integer(c_int8_t), parameter :: PRZM_FLAG_STRIDE = 4
  integer(c_int8_t), parameter :: PRZM_FLAG_NIBBLE = 8
  integer(c_int8_t), parameter :: PRZM_FLAG_EXT_OFFSET = 16
  integer(c_int8_t), parameter :: PRZM_FLAG_BCJ = 32
  integer(c_int8_t), parameter :: PRZM_FLAG_BCJ_SPLIT = 64

  integer(c_size_t), parameter :: PRZM_TLS_SCRATCH_SIZE = 4194304

contains

  function read_u16_le(p) result(res)
    integer(c_int8_t), intent(in) :: p(2)
    integer(c_int16_t) :: res
    res = ior(iand(int(p(1), c_int16_t), 255_c_int16_t), &
              ishft(iand(int(p(2), c_int16_t), 255_c_int16_t), 8))
  end function read_u16_le

  function read_u24_le(p) result(res)
    integer(c_int8_t), intent(in) :: p(3)
    integer(c_int32_t) :: res
    res = ior(ior(iand(int(p(1), c_int32_t), 255), &
                  ishft(iand(int(p(2), c_int32_t), 255), 8)), &
              ishft(iand(int(p(3), c_int32_t), 255), 16))
  end function read_u24_le

  function read_u32_le(p) result(res)
    integer(c_int8_t), intent(in) :: p(4)
    integer(c_int32_t) :: res
    res = ior(ior(ior(iand(int(p(1), c_int32_t), 255), &
                      ishft(iand(int(p(2), c_int32_t), 255), 8)), &
                  ishft(iand(int(p(3), c_int32_t), 255), 16)), &
              ishft(iand(int(p(4), c_int32_t), 255), 24))
  end function read_u32_le

  function read_u64_le(p) result(res)
    integer(c_int8_t), intent(in) :: p(8)
    integer(c_int64_t) :: res
    integer(c_int32_t) :: lo, hi
    lo = read_u32_le(p(1:4))
    hi = read_u32_le(p(5:8))
    res = ior(iand(int(lo, c_int64_t), int(z'FFFFFFFF', c_int64_t)), &
              ishft(iand(int(hi, c_int64_t), int(z'FFFFFFFF', c_int64_t)), 32))
  end function read_u64_le

  subroutine write_u16_le(p, v)
    integer(c_int8_t), intent(out) :: p(2)
    integer(c_int16_t), intent(in) :: v
    p(1) = int(iand(v, 255_c_int16_t), c_int8_t)
    p(2) = int(iand(ishft(v, -8), 255_c_int16_t), c_int8_t)
  end subroutine write_u16_le

  subroutine write_u32_le(p, v)
    integer(c_int8_t), intent(out) :: p(4)
    integer(c_int32_t), intent(in) :: v
    p(1) = int(iand(v, 255), c_int8_t)
    p(2) = int(iand(ishft(v, -8), 255), c_int8_t)
    p(3) = int(iand(ishft(v, -16), 255), c_int8_t)
    p(4) = int(iand(ishft(v, -24), 255), c_int8_t)
  end subroutine write_u32_le

  subroutine write_u64_le(p, v)
    integer(c_int8_t), intent(out) :: p(8)
    integer(c_int64_t), intent(in) :: v
    call write_u32_le(p(1:4), int(iand(v, int(z'FFFFFFFF', c_int64_t)), c_int32_t))
    call write_u32_le(p(5:8), int(iand(ishft(v, -32), int(z'FFFFFFFF', c_int64_t)), c_int32_t))
  end subroutine write_u64_le

  function fast_read_varint(src, src_len, ptr, out_val) result(err)
    integer(c_int8_t), intent(in) :: src(*)
    integer(c_size_t), intent(in) :: src_len
    integer(c_size_t), intent(inout) :: ptr
    integer(c_int32_t), intent(out) :: out_val
    integer(c_int64_t) :: err
    integer(c_int8_t) :: b0, b1, b2, b
    integer(c_int64_t) :: val
    integer :: shift

    err = PRZM_OK
    out_val = 0

    if (ptr > src_len) then
      err = PRZM_ERR_CORRUPTED_FRAME
      return
    end if
    b0 = src(ptr)
    ptr = ptr + 1
    if (b0 >= 0) then
      out_val = b0
      return
    end if

    if (ptr > src_len) then
      err = PRZM_ERR_CORRUPTED_FRAME
      return
    end if
    b1 = src(ptr)
    ptr = ptr + 1
    if (b1 >= 0) then
      out_val = ior(iand(int(b0, c_int32_t), 127), ishft(int(b1, c_int32_t), 7))
      return
    end if

    if (ptr > src_len) then
      err = PRZM_ERR_CORRUPTED_FRAME
      return
    end if
    b2 = src(ptr)
    ptr = ptr + 1
    if (b2 >= 0) then
      out_val = ior(ior(iand(int(b0, c_int32_t), 127), ishft(iand(int(b1, c_int32_t), 127), 7)), ishft(int(b2, c_int32_t), 14))
      return
    end if

    val = ior(ior(iand(int(b0, c_int64_t), 127_c_int64_t), &
                  ishft(iand(int(b1, c_int64_t), 127_c_int64_t), 7)), &
                  ishft(iand(int(b2, c_int64_t), 127_c_int64_t), 14))
    shift = 21

    do while (ptr <= src_len)
      b = src(ptr)
      ptr = ptr + 1
      val = ior(val, ishft(iand(int(b, c_int64_t), 127_c_int64_t), shift))
      if (b >= 0) then
        out_val = int(val, c_int32_t)
        return
      end if
      shift = shift + 7
      if (shift > 63) then
        err = PRZM_ERR_CORRUPTED_FRAME
        return
      end if
    end do
    err = PRZM_ERR_CORRUPTED_FRAME
  end function fast_read_varint

end module przm_types
