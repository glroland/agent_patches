const base = 'h-5 w-5';

export const DashboardIcon = (props) => (
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" className={base} {...props}>
    <rect x="3" y="3" width="8" height="8" rx="1.5" />
    <rect x="13" y="3" width="8" height="5" rx="1.5" />
    <rect x="13" y="10" width="8" height="11" rx="1.5" />
    <rect x="3" y="13" width="8" height="8" rx="1.5" />
  </svg>
);

export const ServerIcon = (props) => (
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" className={base} {...props}>
    <rect x="3" y="4" width="18" height="6" rx="1.5" />
    <rect x="3" y="14" width="18" height="6" rx="1.5" />
    <circle cx="7" cy="7" r="0.8" fill="currentColor" />
    <circle cx="7" cy="17" r="0.8" fill="currentColor" />
  </svg>
);

export const AlertIcon = (props) => (
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" className={base} {...props}>
    <path d="M12 3 2 21h20L12 3Z" strokeLinejoin="round" />
    <path d="M12 9.5v4.5" strokeLinecap="round" />
    <circle cx="12" cy="17.2" r="0.9" fill="currentColor" stroke="none" />
  </svg>
);

export const DiskIcon = (props) => (
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" className={base} {...props}>
    <circle cx="12" cy="12" r="9" />
    <circle cx="12" cy="12" r="2.5" />
    <path d="M12 3v6.5M19.8 16.5 14 13" strokeLinecap="round" />
  </svg>
);

export const PatchIcon = (props) => (
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" className={base} {...props}>
    <path d="M11 4 4 11l3 3 7-7-3-3ZM13 6l5 5M9 14l1.5 1.5M17 6.5 6.5 17 7 20l3-0.5L20 9l-3-2.5Z" strokeLinejoin="round" strokeLinecap="round" />
  </svg>
);

export const UsersIcon = (props) => (
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" className={base} {...props}>
    <circle cx="9" cy="8" r="3" />
    <path d="M2.5 20c0-3.3 2.9-6 6.5-6s6.5 2.7 6.5 6" strokeLinecap="round" />
    <circle cx="17" cy="7" r="2.2" />
    <path d="M15.5 12.2c2.8 0.3 5 2.5 5 5.3" strokeLinecap="round" />
  </svg>
);

export const ChipIcon = (props) => (
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" className={base} {...props}>
    <rect x="6" y="6" width="12" height="12" rx="1.5" />
    <path d="M9 2v3M12 2v3M15 2v3M9 19v3M12 19v3M15 19v3M2 9h3M2 12h3M2 15h3M19 9h3M19 12h3M19 15h3" strokeLinecap="round" />
  </svg>
);

export const NetworkIcon = (props) => (
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" className={base} {...props}>
    <circle cx="5" cy="6" r="2" />
    <circle cx="19" cy="6" r="2" />
    <circle cx="12" cy="18" r="2" />
    <path d="M6.7 7.3 11 17M17.3 7.3 13 17" strokeLinecap="round" />
  </svg>
);

export const ChatIcon = (props) => (
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" className={base} {...props}>
    <path d="M4 5h16v10H9l-4 4V5Z" strokeLinejoin="round" />
  </svg>
);

export const SearchIcon = (props) => (
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" className={base} {...props}>
    <circle cx="11" cy="11" r="7" />
    <path d="m20 20-3-3" strokeLinecap="round" />
  </svg>
);

export const RefreshIcon = (props) => (
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" className={base} {...props}>
    <path d="M4 4v5h5M20 20v-5h-5" strokeLinecap="round" strokeLinejoin="round" />
    <path d="M4.6 15a8 8 0 0 0 13.8 2.5M19.4 9A8 8 0 0 0 5.6 6.5" strokeLinecap="round" />
  </svg>
);
