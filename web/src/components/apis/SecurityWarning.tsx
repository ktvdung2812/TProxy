type Props = {
  message: string;
};

export function SecurityWarning({ message }: Props) {
  return (
    <div className="apis-security-warning">
      <span className="material-symbols-outlined">warning</span>
      <p>{message}</p>
    </div>
  );
}
