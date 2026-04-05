    0: 0xd10203ff  sub sp, sp, #0x80
    4: 0xa9007bfd  stp x29, x30, [sp]
    8: 0x910003fd  mov x29, sp
   12: 0xa90153f3  stp x19, x20, [sp,#16]
   16: 0xa9025bf5  stp x21, x22, [sp,#32]
   20: 0xa90363f7  stp x23, x24, [sp,#48]
   24: 0xa9046bf9  stp x25, x26, [sp,#64]
   28: 0xa90573fb  stp x27, x28, [sp,#80]
   32: 0x6d0627e8  stp d8, d9, [sp,#96]
   36: 0x6d072fea  stp d10, d11, [sp,#112]
   40: 0xaa0003f3  mov x19, x0
   44: 0xf940027a  ldr x26, [x19]
   48: 0xf940067b  ldr x27, [x19,#8]
   52: 0xd2ffffd8  mov x24, #0xfffe000000000000
   56: 0xd2ffffb9  mov x25, #0xfffd000000000000
   60: 0xf9400354  ldr x20, [x26]
   64: 0xd2800000  mov x0, #0x0
   68: 0x9e670004  fmov d4, x0
   72: 0x9e660080  fmov x0, d4
   76: 0xf9004740  str x0, [x26,#136]
   80: 0xd2800035  mov x21, #0x1
   84: 0xd340bea0  ubfx x0, x21, #0, #48
   88: 0xaa180000  orr x0, x0, x24
   92: 0xf9004b40  str x0, [x26,#144]
   96: 0xd2800016  mov x22, #0x0
  100: 0xd340bec0  ubfx x0, x22, #0, #48
  104: 0xaa180000  orr x0, x0, x24
  108: 0xf9004f40  str x0, [x26,#152]
  112: 0x9e660080  fmov x0, d4
  116: 0xf9008f40  str x0, [x26,#280]
  120: 0xaa1603f4  mov x20, x22
  124: 0x140000d4  b .+0x350
  128: 0xd2e7fe00  mov x0, #0x3ff0000000000000
  132: 0x9e670004  fmov d4, x0
  136: 0xf9409740  ldr x0, [x26,#296]
  140: 0x9340bc00  sbfx x0, x0, #0, #48
  144: 0x9e620001  scvtf d1, x0
  148: 0x1e610885  fmul d5, d4, d1
  152: 0x9e6600a0  fmov x0, d5
  156: 0xf9400341  ldr x1, [x26]
  160: 0xd370fc02  lsr x2, x0, #48
  164: 0xd29fffc3  mov x3, #0xfffe
  168: 0xeb03005f  cmp x2, x3
  172: 0x54000081  b.ne .+0x10
  176: 0x9340bc00  sbfx x0, x0, #0, #48
  180: 0x9e620000  scvtf d0, x0
  184: 0x14000002  b .+0x8
  188: 0x9e670000  fmov d0, x0
  192: 0xd370fc22  lsr x2, x1, #48
  196: 0xd29fffc3  mov x3, #0xfffe
  200: 0xeb03005f  cmp x2, x3
  204: 0x54000081  b.ne .+0x10
  208: 0x9340bc21  sbfx x1, x1, #0, #48
  212: 0x9e620021  scvtf d1, x1
  216: 0x14000002  b .+0x8
  220: 0x9e670021  fmov d1, x1
  224: 0x1e611800  fdiv d0, d0, d1
  228: 0x9e660000  fmov x0, d0
  232: 0xf9005b40  str x0, [x26,#176]
  236: 0xd2e7fe00  mov x0, #0x3ff0000000000000
  240: 0x9e670005  fmov d5, x0
  244: 0xf9405b40  ldr x0, [x26,#176]
  248: 0x9e670001  fmov d1, x0
  252: 0x1e6138a6  fsub d6, d5, d1
  256: 0xf9405b40  ldr x0, [x26,#176]
  260: 0x9e670000  fmov d0, x0
  264: 0x1e660805  fmul d5, d0, d6
  268: 0x92800000  mov x0, #0xffffffffffffffff
  272: 0xf900d260  str x0, [x19,#416]
  276: 0xd2800340  mov x0, #0x1a
  280: 0xf9002260  str x0, [x19,#64]
  284: 0xd2800040  mov x0, #0x2
  288: 0xf9002660  str x0, [x19,#72]
  292: 0xd28001c0  mov x0, #0xe
  296: 0xf9002a60  str x0, [x19,#80]
  300: 0xd2800080  mov x0, #0x4
  304: 0xf9000a60  str x0, [x19,#16]
  308: 0x140000bb  b .+0x2ec
  312: 0xf9406b40  ldr x0, [x26,#208]
  316: 0xaa0003f4  mov x20, x0
  320: 0xaa1403e0  mov x0, x20
  324: 0xf9006b40  str x0, [x26,#208]
  328: 0xf9006b54  str x20, [x26,#208]
  332: 0xd2800060  mov x0, #0x3
  336: 0xf9002e60  str x0, [x19,#88]
  340: 0xd2800340  mov x0, #0x1a
  344: 0xf9003260  str x0, [x19,#96]
  348: 0xd2800060  mov x0, #0x3
  352: 0xf9003e60  str x0, [x19,#120]
  356: 0xd2800360  mov x0, #0x1b
  360: 0xf9004260  str x0, [x19,#128]
  364: 0xd28001e0  mov x0, #0xf
  368: 0xf9004660  str x0, [x19,#136]
  372: 0xd28000a0  mov x0, #0x5
  376: 0xf9000a60  str x0, [x19,#16]
  380: 0x140000a9  b .+0x2a4
  384: 0xf9406b54  ldr x20, [x26,#208]
  388: 0xf9406f40  ldr x0, [x26,#216]
  392: 0xaa0003f5  mov x21, x0
  396: 0xf9405b40  ldr x0, [x26,#176]
  400: 0x9e670000  fmov d0, x0
  404: 0xf9405b40  ldr x0, [x26,#176]
  408: 0x9e670001  fmov d1, x0
  412: 0x1e610807  fmul d7, d0, d1
  416: 0x1e6608c4  fmul d4, d6, d6
  420: 0x1e6428e6  fadd d6, d7, d4
  424: 0x1e6508a4  fmul d4, d5, d5
  428: 0x1e6428c5  fadd d5, d6, d4
  432: 0xaa1503e0  mov x0, x21
  436: 0xf9002b40  str x0, [x26,#80]
  440: 0x9e6600a0  fmov x0, d5
  444: 0xf9002f40  str x0, [x26,#88]
  448: 0xf940be63  ldr x3, [x19,#376]
  452: 0xf100c07f  cmp x3, #0x30
  456: 0x5400092a  b.ge .+0x124
  460: 0xf9402b40  ldr x0, [x26,#80]
  464: 0xd370fc01  lsr x1, x0, #48
  468: 0xd29fffe2  mov x2, #0xffff
  472: 0xeb02003f  cmp x1, x2
  476: 0x54000881  b.ne .+0x110
  480: 0xd36cfc01  lsr x1, x0, #44
  484: 0xd28001e2  mov x2, #0xf
  488: 0x8a020021  and x1, x1, x2
  492: 0xf100203f  cmp x1, #0x8
  496: 0x540007e1  b.ne .+0xfc
  500: 0xd340ac00  ubfx x0, x0, #0, #44
  504: 0xf9400001  ldr x1, [x0]
  508: 0xf9408c22  ldr x2, [x1,#280]
  512: 0xb4000762  cbz x2, .+0xec
  516: 0xf9401c23  ldr x3, [x1,#56]
  520: 0xd37df063  lsl x3, x3, #3
  524: 0x9104e063  add x3, x3, #0x138
  528: 0x8b1a0063  add x3, x3, x26
  532: 0xf940b264  ldr x4, [x19,#352]
  536: 0xeb04007f  cmp x3, x4
  540: 0x54000688  b.hi .+0xd0
  544: 0xf9407823  ldr x3, [x1,#240]
  548: 0x91000463  add x3, x3, #0x1
  552: 0xf9007823  str x3, [x1,#240]
  556: 0xf100087f  cmp x3, #0x2
  560: 0x540005e0  b.eq .+0xbc
  564: 0xd10103ff  sub sp, sp, #0x40
  568: 0xa9007bfd  stp x29, x30, [sp]
  572: 0xa9016ffa  stp x26, x27, [sp,#16]
  576: 0xf9409663  ldr x3, [x19,#296]
  580: 0xf90013e3  str x3, [sp,#32]
  584: 0xf9407a63  ldr x3, [x19,#240]
  588: 0xf90017e3  str x3, [sp,#40]
  592: 0xf9408263  ldr x3, [x19,#256]
  596: 0xf9001be3  str x3, [sp,#48]
  600: 0xf9402f43  ldr x3, [x26,#88]
  604: 0xf9009f43  str x3, [x26,#312]
  608: 0x9104e35a  add x26, x26, #0x138
  612: 0xf900027a  str x26, [x19]
  616: 0xf9402c3b  ldr x27, [x1,#88]
  620: 0xf900067b  str x27, [x19,#8]
  624: 0xf9007a60  str x0, [x19,#240]
  628: 0xd2800023  mov x3, #0x1
  632: 0xf9009663  str x3, [x19,#296]
  636: 0xf9409023  ldr x3, [x1,#288]
  640: 0xf9008263  str x3, [x19,#256]
  644: 0xf940be63  ldr x3, [x19,#376]
  648: 0x91000463  add x3, x3, #0x1
  652: 0xf900be63  str x3, [x19,#376]
  656: 0xaa1303e0  mov x0, x19
  660: 0xd63f0040  blr x2
  664: 0xf940be63  ldr x3, [x19,#376]
  668: 0xd1000463  sub x3, x3, #0x1
  672: 0xf900be63  str x3, [x19,#376]
  676: 0xa9416ffa  ldp x26, x27, [sp,#16]
  680: 0xf94013e3  ldr x3, [sp,#32]
  684: 0xf9009663  str x3, [x19,#296]
  688: 0xf94017e3  ldr x3, [sp,#40]
  692: 0xf9007a63  str x3, [x19,#240]
  696: 0xf9401be3  ldr x3, [sp,#48]
  700: 0xf9008263  str x3, [x19,#256]
  704: 0xa9407bfd  ldp x29, x30, [sp]
  708: 0x910103ff  add sp, sp, #0x40
  712: 0xf900027a  str x26, [x19]
  716: 0xf900067b  str x27, [x19,#8]
  720: 0xf9400a63  ldr x3, [x19,#16]
  724: 0xb50000c3  cbnz x3, .+0x18
  728: 0xf9407e60  ldr x0, [x19,#248]
  732: 0xf9002b40  str x0, [x26,#80]
  736: 0xf9402b40  ldr x0, [x26,#80]
  740: 0xaa0003f4  mov x20, x0
  744: 0x14000012  b .+0x48
  748: 0xf9008754  str x20, [x26,#264]
  752: 0xf9006f55  str x21, [x26,#216]
  756: 0xd2800140  mov x0, #0xa
  760: 0xf9001260  str x0, [x19,#32]
  764: 0xd2800020  mov x0, #0x1
  768: 0xf9001660  str x0, [x19,#40]
  772: 0xd2800020  mov x0, #0x1
  776: 0xf9001a60  str x0, [x19,#48]
  780: 0xd28002a0  mov x0, #0x15
  784: 0xf9001e60  str x0, [x19,#56]
  788: 0xd2800060  mov x0, #0x3
  792: 0xf9000a60  str x0, [x19,#16]
  796: 0x14000041  b .+0x104
  800: 0xf9408754  ldr x20, [x26,#264]
  804: 0xf9406f55  ldr x21, [x26,#216]
  808: 0xf9402b40  ldr x0, [x26,#80]
  812: 0xaa0003f4  mov x20, x0
  816: 0xf9408f40  ldr x0, [x26,#280]
  820: 0xaa1403e1  mov x1, x20
  824: 0xd370fc02  lsr x2, x0, #48
  828: 0xd29fffc3  mov x3, #0xfffe
  832: 0xeb03005f  cmp x2, x3
  836: 0x54000161  b.ne .+0x2c
  840: 0xd370fc22  lsr x2, x1, #48
  844: 0xd29fffc3  mov x3, #0xfffe
  848: 0xeb03005f  cmp x2, x3
  852: 0x540001e1  b.ne .+0x3c
  856: 0x9340bc00  sbfx x0, x0, #0, #48
  860: 0x9340bc21  sbfx x1, x1, #0, #48
  864: 0x8b010000  add x0, x0, x1
  868: 0xd340bc00  ubfx x0, x0, #0, #48
  872: 0xaa180000  orr x0, x0, x24
  876: 0x14000010  b .+0x40
  880: 0x9e670000  fmov d0, x0
  884: 0xd370fc22  lsr x2, x1, #48
  888: 0xd29fffc3  mov x3, #0xfffe
  892: 0xeb03005f  cmp x2, x3
  896: 0x54000101  b.ne .+0x20
  900: 0x9340bc21  sbfx x1, x1, #0, #48
  904: 0x9e620021  scvtf d1, x1
  908: 0x14000006  b .+0x18
  912: 0x9340bc00  sbfx x0, x0, #0, #48
  916: 0x9e620000  scvtf d0, x0
  920: 0x9e670021  fmov d1, x1
  924: 0x14000002  b .+0x8
  928: 0x9e670021  fmov d1, x1
  932: 0x1e612800  fadd d0, d0, d1
  936: 0x9e660000  fmov x0, d0
  940: 0xaa0003f5  mov x21, x0
  944: 0xf9008b55  str x21, [x26,#272]
  948: 0x9e6702a4  fmov d4, x21
  952: 0x9e660080  fmov x0, d4
  956: 0xf9008f40  str x0, [x26,#280]
  960: 0xf9409740  ldr x0, [x26,#296]
  964: 0x9340bc14  sbfx x20, x0, #0, #48
  968: 0x14000001  b .+0x4
  972: 0x91000695  add x21, x20, #0x1
  976: 0xd340bea0  ubfx x0, x21, #0, #48
  980: 0xaa180000  orr x0, x0, x24
  984: 0xf9009740  str x0, [x26,#296]
  988: 0xf9400341  ldr x1, [x26]
  992: 0x9340bc21  sbfx x1, x1, #0, #48
  996: 0xeb0102bf  cmp x21, x1
 1000: 0x9a9fc7e0  cset x0, le
 1004: 0xaa190000  orr x0, x0, x25
 1008: 0xaa0003f4  mov x20, x0
 1012: 0x37000054  tbnz w20, #0, .+0x8
 1016: 0x14000002  b .+0x8
 1020: 0x17ffff21  b .+0xfffffffffffffc84
 1024: 0xf9408f40  ldr x0, [x26,#280]
 1028: 0xf9000340  str x0, [x26]
 1032: 0xf9007e60  str x0, [x19,#248]
 1036: 0xf9409661  ldr x1, [x19,#296]
 1040: 0xb50003c1  cbnz x1, .+0x78
 1044: 0x14000001  b .+0x4
 1048: 0xd2800000  mov x0, #0x0
 1052: 0xf9000a60  str x0, [x19,#16]
 1056: 0x6d4627e8  ldp d8, d9, [sp,#96]
 1060: 0x6d472fea  ldp d10, d11, [sp,#112]
 1064: 0xa94573fb  ldp x27, x28, [sp,#80]
 1068: 0xa9446bf9  ldp x25, x26, [sp,#64]
 1072: 0xa94363f7  ldp x23, x24, [sp,#48]
 1076: 0xa9425bf5  ldp x21, x22, [sp,#32]
 1080: 0xa94153f3  ldp x19, x20, [sp,#16]
 1084: 0xa9407bfd  ldp x29, x30, [sp]
 1088: 0x910203ff  add sp, sp, #0x80
 1092: 0xd65f03c0  ret
 1096: 0xd10203ff  sub sp, sp, #0x80
 1100: 0xa9007bfd  stp x29, x30, [sp]
 1104: 0x910003fd  mov x29, sp
 1108: 0xa90153f3  stp x19, x20, [sp,#16]
 1112: 0xa9025bf5  stp x21, x22, [sp,#32]
 1116: 0xa90363f7  stp x23, x24, [sp,#48]
 1120: 0xa9046bf9  stp x25, x26, [sp,#64]
 1124: 0xa90573fb  stp x27, x28, [sp,#80]
 1128: 0x6d0627e8  stp d8, d9, [sp,#96]
 1132: 0x6d072fea  stp d10, d11, [sp,#112]
 1136: 0xaa0003f3  mov x19, x0
 1140: 0xf940027a  ldr x26, [x19]
 1144: 0xf940067b  ldr x27, [x19,#8]
 1148: 0xd2ffffd8  mov x24, #0xfffe000000000000
 1152: 0xd2ffffb9  mov x25, #0xfffd000000000000
 1156: 0x17fffeee  b .+0xfffffffffffffbb8
 1160: 0xd2800000  mov x0, #0x0
 1164: 0xf9000a60  str x0, [x19,#16]
 1168: 0x6d4627e8  ldp d8, d9, [sp,#96]
 1172: 0x6d472fea  ldp d10, d11, [sp,#112]
 1176: 0xa94573fb  ldp x27, x28, [sp,#80]
 1180: 0xa9446bf9  ldp x25, x26, [sp,#64]
 1184: 0xa94363f7  ldp x23, x24, [sp,#48]
 1188: 0xa9425bf5  ldp x21, x22, [sp,#32]
 1192: 0xa94153f3  ldp x19, x20, [sp,#16]
 1196: 0xa9407bfd  ldp x29, x30, [sp]
 1200: 0x910203ff  add sp, sp, #0x80
 1204: 0xd65f03c0  ret
 1208: 0xd10203ff  sub sp, sp, #0x80
 1212: 0xa9007bfd  stp x29, x30, [sp]
 1216: 0x910003fd  mov x29, sp
 1220: 0xa90153f3  stp x19, x20, [sp,#16]
 1224: 0xa9025bf5  stp x21, x22, [sp,#32]
 1228: 0xa90363f7  stp x23, x24, [sp,#48]
 1232: 0xa9046bf9  stp x25, x26, [sp,#64]
 1236: 0xa90573fb  stp x27, x28, [sp,#80]
 1240: 0x6d0627e8  stp d8, d9, [sp,#96]
 1244: 0x6d072fea  stp d10, d11, [sp,#112]
 1248: 0xaa0003f3  mov x19, x0
 1252: 0xf940027a  ldr x26, [x19]
 1256: 0xf940067b  ldr x27, [x19,#8]
 1260: 0xd2ffffd8  mov x24, #0xfffe000000000000
 1264: 0xd2ffffb9  mov x25, #0xfffd000000000000
 1268: 0x17ffff11  b .+0xfffffffffffffc44
 1272: 0xd10203ff  sub sp, sp, #0x80
 1276: 0xa9007bfd  stp x29, x30, [sp]
 1280: 0x910003fd  mov x29, sp
 1284: 0xa90153f3  stp x19, x20, [sp,#16]
 1288: 0xa9025bf5  stp x21, x22, [sp,#32]
 1292: 0xa90363f7  stp x23, x24, [sp,#48]
 1296: 0xa9046bf9  stp x25, x26, [sp,#64]
 1300: 0xa90573fb  stp x27, x28, [sp,#80]
 1304: 0x6d0627e8  stp d8, d9, [sp,#96]
 1308: 0x6d072fea  stp d10, d11, [sp,#112]
 1312: 0xaa0003f3  mov x19, x0
 1316: 0xf940027a  ldr x26, [x19]
 1320: 0xf940067b  ldr x27, [x19,#8]
 1324: 0xd2ffffd8  mov x24, #0xfffe000000000000
 1328: 0xd2ffffb9  mov x25, #0xfffd000000000000
 1332: 0x17ffff13  b .+0xfffffffffffffc4c
 1336: 0xd10203ff  sub sp, sp, #0x80
 1340: 0xa9007bfd  stp x29, x30, [sp]
 1344: 0x910003fd  mov x29, sp
 1348: 0xa90153f3  stp x19, x20, [sp,#16]
 1352: 0xa9025bf5  stp x21, x22, [sp,#32]
 1356: 0xa90363f7  stp x23, x24, [sp,#48]
 1360: 0xa9046bf9  stp x25, x26, [sp,#64]
 1364: 0xa90573fb  stp x27, x28, [sp,#80]
 1368: 0x6d0627e8  stp d8, d9, [sp,#96]
 1372: 0x6d072fea  stp d10, d11, [sp,#112]
 1376: 0xaa0003f3  mov x19, x0
 1380: 0xf940027a  ldr x26, [x19]
 1384: 0xf940067b  ldr x27, [x19,#8]
 1388: 0xd2ffffd8  mov x24, #0xfffe000000000000
 1392: 0xd2ffffb9  mov x25, #0xfffd000000000000
 1396: 0x17ffff6b  b .+0xfffffffffffffdac
