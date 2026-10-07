import { Link } from 'react-router-dom';
import { Empty } from '../components/ui';

export default function NotFound() {
  return <div className="page"><Empty title="Not found"><Link to="/">Back to chat</Link></Empty></div>;
}
