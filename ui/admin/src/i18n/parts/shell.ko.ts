import type { shellEn } from "./shell.en";

export const shellKo: Record<keyof typeof shellEn, string> = {
  "unsaved.title": "저장하지 않은 변경을 버릴까요?",
  "unsaved.text": "지금 나가면 저장하지 않은 변경 내용이 사라집니다.",
  "unsaved.discard": "버리기",

  "palette.open": "명령 팔레트",
  "palette.title": "이동하거나 명령 실행",
  "palette.placeholder": "컬렉션, 화면, 명령 검색…",
  "palette.empty": "일치하는 항목이 없습니다.",
  "palette.group.recent": "최근 컬렉션",
  "palette.group.collections": "컬렉션",
  "palette.group.screens": "화면",
  "palette.group.actions": "명령",
  "palette.group.users": "사용자",
  "palette.usersLoading": "사용자 검색 중…",
  "palette.usersFailed": "사용자를 검색하지 못했습니다: {error}",
  "palette.newCollection": "새 컬렉션",
  "palette.newRecord": "{name}에 새 레코드",
  "palette.theme": "테마: {mode}",
  "palette.language": "언어: {name}",
  "palette.signOut": "로그아웃",
  "palette.shortcuts": "키보드 단축키",

  "shortcuts.title": "키보드 단축키",
  "shortcuts.palette": "명령 팔레트",
  "shortcuts.help": "이 목록 보기",
  "shortcuts.filter": "검색·필터 입력으로 이동",
  "shortcuts.new": "이 화면에서 새 항목 만들기",
  "shortcuts.close": "열린 패널이나 대화상자 닫기",
  "shortcuts.save": "변경 내용 저장",
  "shortcuts.then": "다음",
  "shortcuts.group.general": "일반",
  "shortcuts.group.navigation": "이동",


  "field.reveal": "표시",
  "field.capsLock": "Caps Lock이 켜져 있습니다.",
};
